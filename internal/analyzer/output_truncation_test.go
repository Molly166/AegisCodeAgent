package analyzer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Molly166/AegisCodeAgent/internal/githubreport"
	"github.com/Molly166/AegisCodeAgent/internal/review"
)

func TestAllAdaptersRejectTruncatedCustomRunnerOutput(t *testing.T) {
	for _, test := range []struct {
		name, tool, output string
		create             func(Runner) Analyzer
	}{
		{NameGoTest, "go", `{"Action":"output","Output":"./main_test.go:2: earlier failure\n"}`, func(r Runner) Analyzer { return NewGoTestAnalyzer(r) }},
		{NameGoVet, "go", `{"sample":{"printf":[{"posn":"/repo/main.go:2:1","message":"earlier diagnostic"}]}}`, func(r Runner) Analyzer { return NewGoVetAnalyzer(r) }},
		{NameStaticcheck, NameStaticcheck, `{"code":"SA5001","location":{"file":"/repo/main.go","line":2},"message":"earlier diagnostic"}`, func(r Runner) Analyzer { return NewStaticcheckAnalyzer(r) }},
		{NameGosec, NameGosec, `{"Issues":[{"severity":"HIGH","confidence":"HIGH","rule_id":"G204","details":"earlier diagnostic","file":"/repo/main.go","line":"2"}]}`, func(r Runner) Analyzer { return NewGosecAnalyzer(r) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			// A custom runner may not have adopted the sentinel error contract.
			// Complete earlier stdout remains readable while later stderr was cut.
			runner := &fakeRunner{available: map[string]bool{test.tool: true}, executions: map[string]Execution{test.tool: {Stdout: test.output, Truncated: true, ExitCode: 0}}}
			findings, err := test.create(runner).Analyze(context.Background(), Input{Repository: "/repo", Packages: []string{"."}})
			if !errors.Is(err, ErrOutputTruncated) || len(findings) != 1 {
				t.Fatalf("truncated output accepted or earlier evidence lost: findings=%+v err=%v", findings, err)
			}
		})
	}
}

func TestTruncatedP2PrefixCannotHideLaterP1AndPassMergeGate(t *testing.T) {
	// The generic diagnostic prefix maps to P2. A later SA5001/P1 diagnostic
	// lies past the capture limit; the remaining low-priority prefix is unsafe
	// to treat as a complete successful staticcheck run.
	early := `{"code":"OTHER","location":{"file":"/repo/main.go","line":2},"message":"earlier generic diagnostic"}`
	late := `{"code":"SA5001","location":{"file":"/repo/main.go","line":50},"message":"unchecked error before close"}`
	buffer := newLimitedBuffer(len(early) + 4)
	buffer.Write([]byte(early + "\n" + late))
	runner := &fakeRunner{available: map[string]bool{NameStaticcheck: true}, executions: map[string]Execution{NameStaticcheck: {Stdout: buffer.String(), Truncated: buffer.Truncated(), ExitCode: 1}}}
	pipeline := NewPipeline(NewStaticcheckAnalyzer(runner), pipelineStub{name: "passing"})
	output, err := pipeline.Run(context.Background(), Input{Repository: "/repo", Packages: []string{"."}}, []string{NameStaticcheck, "passing"}, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(output.Findings) != 1 || output.Findings[0].Severity != review.SeverityMedium {
		t.Fatalf("fixture no longer retains only P2: %+v", output.Findings)
	}
	if output.Analysis.Status != review.AnalysisPartial || output.Analysis.Tools[0].Status != review.ToolFailed || !strings.Contains(output.Analysis.Tools[0].Detail, "capture limit") {
		t.Fatalf("truncated analysis looks complete: %+v", output.Analysis)
	}
	report := review.NewReport(review.Comparison{}, nil, output.Findings)
	report.Analysis = output.Analysis
	gate := githubreport.Evaluate(report, githubreport.PriorityP1, true)
	if !gate.Blocked || !gate.Incomplete || gate.BlockedByFinding {
		t.Fatalf("truncation must block for incompleteness, not promote P2: %+v", gate)
	}
}

func TestOSRunnerReturnsTruncationErrorForSuccessfulAndFailedProcesses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	for _, exit := range []string{"0", "7"} {
		t.Run("exit "+exit, func(t *testing.T) {
			directory := t.TempDir()
			script := filepath.Join(directory, "noisy.sh")
			if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '0123456789abcdef0123456789abcdef'\nexit "+exit+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			execution, err := (OSRunner{MaxOutputBytes: 8}).Run(context.Background(), Command{Name: script, Directory: directory})
			if !errors.Is(err, ErrOutputTruncated) || !execution.Truncated || !strings.HasPrefix(execution.Stdout, "01234567") {
				t.Fatalf("low capture limit failed open: %+v %v", execution, err)
			}
			if exit == "7" && execution.ExitCode != 7 {
				t.Fatalf("underlying exit code lost: %+v", execution)
			}
		})
	}
}

func TestDockerRunnerReturnsTruncationErrorAtProcessBoundary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	repository, cache := t.TempDir(), t.TempDir()
	repository, _ = filepath.EvalSymlinks(repository)
	fakeDocker := filepath.Join(t.TempDir(), "docker-fixture")
	if err := os.WriteFile(fakeDocker, []byte("#!/bin/sh\nprintf '0123456789abcdef0123456789abcdef'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	runner := &DockerRunner{Repository: repository, Cache: cache, Image: "aegis-analysis:local", MaxOutputBytes: 8, docker: fakeDocker}
	execution, err := runner.Run(context.Background(), Command{Name: "go", Arguments: []string{"version"}, Directory: repository})
	if !errors.Is(err, ErrOutputTruncated) || !execution.Truncated || execution.ExitCode != 0 {
		t.Fatalf("Docker process boundary hid truncation: %+v %v", execution, err)
	}
}

func TestTruncationPreservesOtherErrorIdentityAndExactLimitIsComplete(t *testing.T) {
	err := executionError(Execution{Truncated: true}, context.Canceled)
	if !errors.Is(err, ErrOutputTruncated) || !errors.Is(err, context.Canceled) {
		t.Fatalf("lost error identity: %v", err)
	}
	buffer := newLimitedBuffer(3)
	buffer.Write([]byte("abc"))
	buffer.Write(nil)
	if buffer.Truncated() {
		t.Fatal("exactly fitting output marked truncated")
	}
}
