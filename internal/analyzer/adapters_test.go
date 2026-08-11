package analyzer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

func TestGoTestAnalyzerParsesJSONDiagnostic(t *testing.T) {
	runner := &fakeRunner{
		available: map[string]bool{"go": true},
		executions: map[string]Execution{"go": {
			ExitCode: 1,
			Stdout: strings.Join([]string{
				`{"Action":"output","Package":"example.com/app","Output":"./main_test.go:12: expected 1, got 2\n"}`,
				`{"Action":"fail","Package":"example.com/app","Test":"TestValue"}`,
			}, "\n"),
		}},
	}
	analyzer := NewGoTestAnalyzer(runner)
	findings, err := analyzer.Analyze(context.Background(), Input{Repository: "/repo", Packages: []string{"."}})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("finding count = %d, want 1: %+v", len(findings), findings)
	}
	finding := findings[0]
	if finding.Source != NameGoTest || finding.Location.Path != "main_test.go" || finding.Location.StartLine != 12 {
		t.Fatalf("unexpected finding: %+v", finding)
	}
	if finding.Severity != review.SeverityHigh || finding.Confidence != 1 {
		t.Fatalf("unexpected classification: %+v", finding)
	}
}

func TestGoTestAnalyzerCreatesSummaryForUnlocatedFailure(t *testing.T) {
	runner := &fakeRunner{
		available:  map[string]bool{"go": true},
		executions: map[string]Execution{"go": {ExitCode: 1, Stderr: "package failed without a source position"}},
	}
	findings, err := NewGoTestAnalyzer(runner).Analyze(context.Background(), Input{Repository: "/repo", Packages: []string{"./..."}})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Location.Path != "" || !strings.Contains(findings[0].Evidence, "package failed") {
		t.Fatalf("unexpected summary finding: %+v", findings)
	}
}

func TestGoVetAnalyzerParsesJSON(t *testing.T) {
	runner := &fakeRunner{
		available: map[string]bool{"go": true},
		executions: map[string]Execution{"go": {
			ExitCode: 1,
			Stderr:   `{"example.com/app":{"printf":[{"posn":"/repo/main.go:7:2","message":"fmt.Printf format %d has arg name of wrong type string"}]}}`,
		}},
	}
	findings, err := NewGoVetAnalyzer(runner).Analyze(context.Background(), Input{Repository: "/repo", Packages: []string{"."}})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if len(findings) != 1 || findings[0].RuleID != "printf" || findings[0].Location.StartLine != 7 {
		t.Fatalf("unexpected findings: %+v", findings)
	}
}

func TestStaticcheckAnalyzerParsesJSONLines(t *testing.T) {
	runner := &fakeRunner{
		available: map[string]bool{NameStaticcheck: true},
		executions: map[string]Execution{NameStaticcheck: {
			ExitCode: 1,
			Stdout:   `{"code":"SA5001","severity":"error","location":{"file":"/repo/server.go","line":24,"column":3},"end":{"file":"/repo/server.go","line":24,"column":12},"message":"should check returned error before deferring Close"}`,
		}},
	}
	findings, err := NewStaticcheckAnalyzer(runner).Analyze(context.Background(), Input{Repository: "/repo", Packages: []string{"."}})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("finding count = %d, want 1", len(findings))
	}
	finding := findings[0]
	if finding.RuleID != "SA5001" || finding.Severity != review.SeverityHigh || finding.Location.Path != "server.go" {
		t.Fatalf("unexpected finding: %+v", finding)
	}
}

func TestGosecAnalyzerParsesJSON(t *testing.T) {
	runner := &fakeRunner{
		available: map[string]bool{NameGosec: true},
		executions: map[string]Execution{NameGosec: {
			ExitCode: 1,
			Stdout:   `{"Issues":[{"severity":"HIGH","confidence":"HIGH","rule_id":"G204","details":"Subprocess launched with variable","file":"/repo/exec.go","code":"cmd := exec.Command(name)","line":"18-19"}]}`,
		}},
	}
	findings, err := NewGosecAnalyzer(runner).Analyze(context.Background(), Input{Repository: "/repo", Packages: []string{"."}})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("finding count = %d, want 1", len(findings))
	}
	finding := findings[0]
	if finding.RuleID != "G204" || finding.Severity != review.SeverityHigh || finding.Confidence != 0.99 {
		t.Fatalf("unexpected finding: %+v", finding)
	}
	if finding.Location.StartLine != 18 || finding.Location.EndLine != 19 {
		t.Fatalf("unexpected location: %+v", finding.Location)
	}
}

func TestExternalAnalyzerReportsUnavailableTool(t *testing.T) {
	runner := &fakeRunner{available: map[string]bool{}}
	_, err := NewStaticcheckAnalyzer(runner).Analyze(context.Background(), Input{Packages: []string{"."}})
	var skipError *SkipError
	if err == nil || !strings.Contains(err.Error(), "not installed") || !errorsAs(err, &skipError) || skipError.Kind != SkipUnavailable {
		t.Fatalf("unexpected error: %T %v", err, err)
	}
}

func errorsAs(err error, target any) bool {
	return errors.As(err, target)
}
