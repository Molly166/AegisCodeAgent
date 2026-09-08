package analyzer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

// These are protocol fixtures, not live skipped tests: the reviewer must use
// terminal Go events as evidence, never a diagnostic-looking log line alone.
func TestGoTestEventsRequireFailureEvidence(t *testing.T) {
	tests := []struct {
		name         string
		events       []string
		stderr       string
		exitCode     int
		truncated    bool
		runErr       error
		wantCount    int
		wantErr      error
		wantAnyError bool
		wantEvidence []string
		denyEvidence []string
		wantTitles   []string
	}{
		{
			name: "Docker opt-in skip is not a finding",
			events: []string{
				`{"Action":"run","Package":"example.com/app/internal/analyzer","Test":"TestDockerIntegration"}`,
				`{"Action":"output","Package":"example.com/app/internal/analyzer","Test":"TestDockerIntegration","Output":"    docker_integration_test.go:20: set AEGIS_DOCKER_TEST_IMAGE on Linux to exercise Docker isolation\n"}`,
				`{"Action":"skip","Package":"example.com/app/internal/analyzer","Test":"TestDockerIntegration"}`,
				`{"Action":"pass","Package":"example.com/app/internal/analyzer"}`,
			},
		},
		{
			name: "passing diagnostic-shaped log is not a finding",
			events: []string{
				`{"Action":"output","Package":"example.com/app","Test":"TestHealthy","Output":"    healthy_test.go:12: input validation completed\n"}`,
				`{"Action":"pass","Package":"example.com/app","Test":"TestHealthy"}`,
				`{"Action":"pass","Package":"example.com/app"}`,
			},
		},
		{
			name: "unknown OutputType cannot override passing terminal event",
			events: []string{
				`{"Action":"output","Package":"example.com/app","Test":"TestHealthy","OutputType":"error","Output":"    healthy_test.go:12: diagnostic from a successful fixture\n"}`,
				`{"Action":"pass","Package":"example.com/app","Test":"TestHealthy"}`,
				`{"Action":"pass","Package":"example.com/app"}`,
			},
		},
		{
			name: "package without tests is not a finding",
			events: []string{
				`{"Action":"output","Package":"example.com/empty","Output":"?\texample.com/empty\t[no test files]\n"}`,
				`{"Action":"skip","Package":"example.com/empty"}`,
			},
		},
		{
			name:     "mixed failing skipped and passing tests do not share failure state",
			exitCode: 1,
			events: []string{
				`{"Action":"output","Package":"example.com/app","Test":"TestSkipped","Output":"    skip_test.go:20: skip sentinel\n"}`,
				`{"Action":"skip","Package":"example.com/app","Test":"TestSkipped"}`,
				`{"Action":"output","Package":"example.com/app","Test":"TestHealthy","Output":"    healthy_test.go:12: healthy sentinel\n"}`,
				`{"Action":"pass","Package":"example.com/app","Test":"TestHealthy"}`,
				`{"Action":"output","Package":"example.com/app","Test":"TestBroken","Output":"    broken_test.go:15: actual failed assertion\n"}`,
				`{"Action":"fail","Package":"example.com/app","Test":"TestBroken"}`,
				`{"Action":"fail","Package":"example.com/app"}`,
			},
			wantCount: 1, wantTitles: []string{"TestBroken"},
			wantEvidence: []string{"actual failed assertion"}, denyEvidence: []string{"skip sentinel", "healthy sentinel"},
		},
		{
			name:     "same test name in different packages stays isolated",
			exitCode: 1,
			events: []string{
				`{"Action":"output","Package":"example.com/broken","Test":"TestValue","Output":"    value_test.go:11: broken package assertion\n"}`,
				`{"Action":"output","Package":"example.com/healthy","Test":"TestValue","Output":"    value_test.go:11: healthy package log\n"}`,
				`{"Action":"pass","Package":"example.com/healthy","Test":"TestValue"}`,
				`{"Action":"pass","Package":"example.com/healthy"}`,
				`{"Action":"fail","Package":"example.com/broken","Test":"TestValue"}`,
				`{"Action":"fail","Package":"example.com/broken"}`,
			},
			wantCount: 1, wantTitles: []string{"TestValue"},
			wantEvidence: []string{"broken package assertion"}, denyEvidence: []string{"healthy package log"},
		},
		{
			name:     "two packages with the same failing test produce separate evidence",
			exitCode: 1,
			events: []string{
				`{"Action":"output","Package":"example.com/first","Test":"TestValue","Output":"    value_test.go:11: first package assertion\n"}`,
				`{"Action":"output","Package":"example.com/second","Test":"TestValue","Output":"    value_test.go:11: second package assertion\n"}`,
				`{"Action":"fail","Package":"example.com/second","Test":"TestValue"}`,
				`{"Action":"fail","Package":"example.com/first","Test":"TestValue"}`,
				`{"Action":"fail","Package":"example.com/first"}`,
				`{"Action":"fail","Package":"example.com/second"}`,
			},
			wantCount: 2, wantEvidence: []string{"first package assertion", "second package assertion"},
		},
		{
			name:     "child failure does not create cascading parent and package findings",
			exitCode: 1,
			events: []string{
				`{"Action":"run","Package":"example.com/app","Test":"TestGroup"}`,
				`{"Action":"output","Package":"example.com/app","Test":"TestGroup/broken","Output":"    group_test.go:31: failing child assertion\n"}`,
				`{"Action":"fail","Package":"example.com/app","Test":"TestGroup/broken"}`,
				`{"Action":"output","Package":"example.com/app","Test":"TestGroup/optional","Output":"    group_test.go:40: optional child skipped\n"}`,
				`{"Action":"skip","Package":"example.com/app","Test":"TestGroup/optional"}`,
				`{"Action":"output","Package":"example.com/app","Test":"TestGroup","Output":"--- FAIL: TestGroup (0.00s)\n"}`,
				`{"Action":"fail","Package":"example.com/app","Test":"TestGroup"}`,
				`{"Action":"fail","Package":"example.com/app"}`,
			},
			wantCount: 1, wantTitles: []string{"TestGroup/broken"},
			wantEvidence: []string{"failing child assertion"}, denyEvidence: []string{"optional child skipped"},
		},
		{
			name:     "one failing test does not turn every log line into a separate finding",
			exitCode: 1,
			events: []string{
				`{"Action":"output","Package":"example.com/app","Test":"TestBroken","Output":"    broken_test.go:10: setup log\n    broken_test.go:20: failing assertion\n"}`,
				`{"Action":"fail","Package":"example.com/app","Test":"TestBroken"}`,
				`{"Action":"fail","Package":"example.com/app"}`,
			},
			wantCount: 1, wantTitles: []string{"TestBroken"}, wantEvidence: []string{"failing assertion"},
		},
		{
			name:     "build failure uses ImportPath and FailedBuild without duplicate package failure",
			exitCode: 1,
			events: []string{
				`{"ImportPath":"example.com/app [example.com/app.test]","Action":"build-output","Output":"# example.com/app [example.com/app.test]\nmain_test.go:3:11: undefined: missingValue\n"}`,
				`{"ImportPath":"example.com/app [example.com/app.test]","Action":"build-fail"}`,
				`{"Action":"output","Package":"example.com/app","Output":"FAIL\texample.com/app [build failed]\n"}`,
				`{"Action":"fail","Package":"example.com/app","FailedBuild":"example.com/app [example.com/app.test]"}`,
			},
			wantCount: 1, wantEvidence: []string{"undefined: missingValue"},
		},
		{
			name:     "interleaved successful build output cannot attach to a failing build",
			exitCode: 1,
			events: []string{
				`{"ImportPath":"example.com/healthy","Action":"build-output","Output":"healthy.go:8:3: unrelated toolchain warning\n"}`,
				`{"ImportPath":"example.com/broken","Action":"build-output","Output":"broken.go:3:11: undefined: missingValue\n"}`,
				`{"ImportPath":"example.com/broken","Action":"build-fail"}`,
				`{"Action":"skip","Package":"example.com/healthy"}`,
				`{"Action":"fail","Package":"example.com/broken","FailedBuild":"example.com/broken"}`,
			},
			wantCount: 1, wantEvidence: []string{"undefined: missingValue"}, denyEvidence: []string{"unrelated toolchain warning"},
		},
		{
			name:     "build output fragments reconstruct the complete failure evidence",
			exitCode: 1,
			events: []string{
				`{"ImportPath":"example.com/app","Action":"build-output","Output":"main.go:3:11: undefined: missing"}`,
				`{"ImportPath":"example.com/app","Action":"build-output","Output":"Value\n"}`,
				`{"ImportPath":"example.com/app","Action":"build-fail"}`,
				`{"Action":"fail","Package":"example.com/app","FailedBuild":"example.com/app"}`,
			},
			wantCount: 1, wantEvidence: []string{"undefined: missingValue"},
		},
		{
			name:     "build failure without compiler output still has a finding",
			exitCode: 1,
			events: []string{
				`{"ImportPath":"example.com/dependency","Action":"build-fail"}`,
				`{"Action":"fail","Package":"example.com/app","FailedBuild":"example.com/dependency"}`,
			},
			wantCount: 1,
		},
		{
			name:     "failing test output fragments reconstruct the complete evidence",
			exitCode: 1,
			events: []string{
				`{"Action":"output","Package":"example.com/app","Test":"TestBroken","Output":"    main_test.go:12: expected 1, "}`,
				`{"Action":"output","Package":"example.com/app","Test":"TestBroken","Output":"got 2\n"}`,
				`{"Action":"fail","Package":"example.com/app","Test":"TestBroken"}`,
				`{"Action":"fail","Package":"example.com/app"}`,
			},
			wantCount: 1, wantEvidence: []string{"expected 1, got 2"},
		},
		{
			name:     "legacy raw stderr compiler failure retains a conservative finding",
			exitCode: 1, stderr: "# example.com/app\nmain.go:3:11: undefined: missingValue\n",
			wantCount: 1, wantEvidence: []string{"undefined: missingValue"},
		},
		{
			name:   "successful raw stderr diagnostic-looking output is not a finding",
			stderr: "main.go:3:11: toolchain informational message\n",
			events: []string{`{"Action":"pass","Package":"example.com/app"}`},
		},
		{
			name:     "failure without source coordinates still has evidence",
			exitCode: 1,
			events: []string{
				`{"Action":"output","Package":"example.com/app","Test":"TestCrash","Output":"panic: fixture crash\n"}`,
				`{"Action":"fail","Package":"example.com/app","Test":"TestCrash"}`,
				`{"Action":"fail","Package":"example.com/app"}`,
			},
			wantCount: 1, wantTitles: []string{"TestCrash"}, wantEvidence: []string{"panic: fixture crash"},
		},
		{
			name:     "unlocated nonzero exit remains blocking",
			exitCode: 2, stderr: "go: package setup failed before tests could start\n",
			wantCount: 1, wantEvidence: []string{"package setup failed"},
		},
		{
			name:      "truncated log without a terminal failure is incomplete not fabricated P1",
			truncated: true,
			events: []string{
				`{"Action":"output","Package":"example.com/app","Test":"TestUnknown","Output":"    unknown_test.go:12: diagnostic-looking prefix\n"}`,
			},
			wantErr: ErrOutputTruncated,
		},
		{
			name:      "explicit failure survives later output truncation",
			truncated: true,
			events: []string{
				`{"Action":"output","Package":"example.com/app","Test":"TestBroken","Output":"    broken_test.go:12: confirmed failure before truncation\n"}`,
				`{"Action":"fail","Package":"example.com/app","Test":"TestBroken"}`,
			},
			wantErr: ErrOutputTruncated, wantCount: 1, wantEvidence: []string{"confirmed failure before truncation"},
		},
		{
			name:   "cancelled log without terminal failure is not fabricated P1",
			runErr: context.Canceled,
			events: []string{
				`{"Action":"output","Package":"example.com/app","Test":"TestUnknown","Output":"    unknown_test.go:12: diagnostic-looking prefix\n"}`,
			},
			wantErr: context.Canceled,
		},
		{
			name:   "explicit failure survives later cancellation",
			runErr: context.Canceled,
			events: []string{
				`{"Action":"output","Package":"example.com/app","Test":"TestBroken","Output":"    broken_test.go:12: confirmed failure before cancellation\n"}`,
				`{"Action":"fail","Package":"example.com/app","Test":"TestBroken"}`,
			},
			wantErr: context.Canceled, wantCount: 1, wantEvidence: []string{"confirmed failure before cancellation"},
		},
		{
			name:      "deadline and truncation identities are not discarded",
			truncated: true, runErr: context.DeadlineExceeded,
			wantErr: context.DeadlineExceeded,
		},
		{
			name: "malformed JSON marks analysis incomplete",
			events: []string{
				`{"Action":"output","Package":"example.com/app","Test":"TestUnknown","Output":`,
			},
			wantAnyError: true,
		},
		{
			name: "build output alone cannot prove a successful test execution",
			events: []string{
				`{"ImportPath":"example.com/app","Action":"build-output","Output":"main.go:3:11: toolchain warning\n"}`,
			},
			wantAnyError: true,
		},
		{
			name: "damaged stdout cannot hide beside a valid passing event",
			events: []string{
				`damaged protocol output`,
				`{"Action":"pass","Package":"example.com/app"}`,
			},
			wantAnyError: true,
		},
		{
			name: "non-object JSON cannot hide beside a valid passing event",
			events: []string{
				`["unexpected JSON array"]`,
				`{"Action":"pass","Package":"example.com/app"}`,
			},
			wantAnyError: true,
		},
		{
			name: "explicit failure survives later malformed JSON",
			events: []string{
				`{"Action":"output","Package":"example.com/app","Test":"TestBroken","Output":"    broken_test.go:12: confirmed failure before malformed event\n"}`,
				`{"Action":"fail","Package":"example.com/app","Test":"TestBroken"}`,
				`{"Action":"output","Package":"example.com/app","Output":`,
			},
			wantAnyError: true, wantCount: 1, wantEvidence: []string{"confirmed failure before malformed event"},
		},
		{
			name: "unfinished test is not silently accepted after successful process exit",
			events: []string{
				`{"Action":"run","Package":"example.com/app","Test":"TestUnknown"}`,
				`{"Action":"output","Package":"example.com/app","Test":"TestUnknown","Output":"    unknown_test.go:12: diagnostic-looking prefix\n"}`,
			},
			wantAnyError: true,
		},
		{
			name: "package pass does not conceal an unfinished individual test",
			events: []string{
				`{"Action":"run","Package":"example.com/app","Test":"TestUnknown"}`,
				`{"Action":"pass","Package":"example.com/app"}`,
			},
			wantAnyError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeRunner{
				available: map[string]bool{"go": true},
				executions: map[string]Execution{"go": {
					Stdout: strings.Join(test.events, "\n"), Stderr: test.stderr,
					ExitCode: test.exitCode, Truncated: test.truncated,
				}},
				errors: map[string]error{"go": test.runErr},
			}
			findings, err := NewGoTestAnalyzer(runner).Analyze(context.Background(), Input{Repository: t.TempDir(), Packages: []string{"./..."}})
			switch {
			case test.wantErr != nil:
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("error = %v, want identity %v", err, test.wantErr)
				}
			case test.wantAnyError:
				if err == nil {
					t.Fatal("incomplete event stream was accepted")
				}
			case err != nil:
				t.Fatalf("unexpected analysis error: %v", err)
			}
			if test.truncated && !errors.Is(err, ErrOutputTruncated) {
				t.Fatalf("truncation identity lost: %v", err)
			}
			if len(findings) != test.wantCount {
				t.Fatalf("got %d findings, want %d: %+v", len(findings), test.wantCount, findings)
			}
			var evidence, titles strings.Builder
			for _, finding := range findings {
				if finding.Source != NameGoTest || finding.Severity != review.SeverityHigh {
					t.Errorf("unexpected failure classification: %+v", finding)
				}
				if finding.Location.Path != "" {
					t.Errorf("location was guessed without matching repository source: %+v", finding.Location)
				}
				evidence.WriteString(finding.Evidence + "\n")
				titles.WriteString(finding.Title + "\n")
			}
			for _, expected := range test.wantEvidence {
				if !strings.Contains(evidence.String(), expected) {
					t.Errorf("evidence %q does not contain %q", evidence.String(), expected)
				}
			}
			for _, forbidden := range test.denyEvidence {
				if strings.Contains(evidence.String(), forbidden) {
					t.Errorf("unrelated successful/skipped output leaked into failure evidence: %q", forbidden)
				}
			}
			for _, expected := range test.wantTitles {
				if !strings.Contains(titles.String(), expected) {
					t.Errorf("titles %q do not name failed test %q", titles.String(), expected)
				}
			}
		})
	}
}
