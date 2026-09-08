package analyzer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

func TestGoTestLocationRequiresUniqueRepositorySource(t *testing.T) {
	repository := t.TempDir()
	const source = "package fixture\n\nfunc value() int {\n\treturn 1\n}\n"
	for _, relative := range []string{"value_test.go", "internal/first/value_test.go", "internal/second/value_test.go"} {
		path := filepath.Join(repository, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(repository, "directory.go"), 0755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside_test.go")
	if err := os.WriteFile(outside, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		pkg    string
		output string
		want   review.Location
	}{
		{
			name: "single root-package source position",
			pkg:  "example.com/app", output: "    value_test.go:4: observed value\n",
			want: review.Location{Path: "value_test.go", StartLine: 4},
		},
		{
			name: "repeated identical source positions are still unique",
			pkg:  "example.com/app", output: "    value_test.go:4: first log\n    value_test.go:4: second log\n",
			want: review.Location{Path: "value_test.go", StartLine: 4},
		},
		{
			name: "multiple source positions do not identify the failed assertion",
			pkg:  "example.com/app", output: "    value_test.go:3: setup log\n    value_test.go:4: later log\n",
		},
		{
			name: "missing file does not become a guessed annotation",
			pkg:  "example.com/app", output: "    missing_test.go:4: observed value\n",
		},
		{
			name: "line after source end is not a location",
			pkg:  "example.com/app", output: "    value_test.go:99: observed value\n",
		},
		{
			name: "line zero is not a location",
			pkg:  "example.com/app", output: "    value_test.go:0: observed value\n",
		},
		{
			name: "directory with Go suffix is not a regular source file",
			pkg:  "example.com/app", output: "    directory.go:1: observed value\n",
		},
		{
			name: "module subpackage resolves its own same-basename source",
			pkg:  "example.com/app/internal/first", output: "    value_test.go:4: observed value\n",
			want: review.Location{Path: "internal/first/value_test.go", StartLine: 4},
		},
		{
			name: "different subpackage does not resolve first matching basename",
			pkg:  "example.com/app/internal/second", output: "    value_test.go:4: observed value\n",
			want: review.Location{Path: "internal/second/value_test.go", StartLine: 4},
		},
		{
			name:   "absolute repository source does not receive a second package prefix",
			pkg:    "example.com/app/internal/first",
			output: fmt.Sprintf("    %s:4: observed value\n", filepath.Join(repository, "internal/first/value_test.go")),
			want:   review.Location{Path: "internal/first/value_test.go", StartLine: 4},
		},
		{
			name: "absolute source outside repository cannot be annotated",
			pkg:  "example.com/app", output: fmt.Sprintf("    %s:4: observed value\n", outside),
		},
		{
			name: "relative parent traversal cannot be annotated",
			pkg:  "example.com/app", output: "    ../outside_test.go:4: observed value\n",
		},
		{
			name: "dependency import path cannot borrow a same-basename local file",
			pkg:  "example.com/dependency", output: "    value_test.go:4: observed value\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := goTestLocation(repository, "example.com/app", test.pkg, test.output)
			if got != test.want {
				t.Fatalf("goTestLocation() = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestGoTestLocationRejectsEscapingSymlinks(t *testing.T) {
	repository, outside := t.TempDir(), t.TempDir()
	const source = "package fixture\n\nfunc value() int {\n\treturn 1\n}\n"
	outsideFile := filepath.Join(outside, "outside_test.go")
	if err := os.WriteFile(outsideFile, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(repository, "escape_test.go")); err != nil {
		t.Skipf("symlink fixture is unavailable on this host: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(repository, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"escape_test.go", "escape/outside_test.go"} {
		t.Run(name, func(t *testing.T) {
			got := goTestLocation(repository, "example.com/app", "example.com/app", name+":4: observed value\n")
			if got != (review.Location{}) {
				t.Fatalf("source escaped repository through symlink: %+v", got)
			}
		})
	}
}

func TestGoTestBuildFallbackEvidenceDoesNotGuessSourceOwnership(t *testing.T) {
	repository := t.TempDir()
	const source = "package fixture\n\nfunc value() int {\n\treturn 1\n}\n"
	if err := os.WriteFile(filepath.Join(repository, "value_test.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{
		available: map[string]bool{"go": true},
		executions: map[string]Execution{"go": {
			ExitCode: 1,
			Stdout: strings.Join([]string{
				`{"ImportPath":"example.com/broken","Action":"build-fail"}`,
				`{"Action":"fail","Package":"example.com/broken","FailedBuild":"example.com/broken"}`,
			}, "\n"),
			Stderr: "value_test.go:4: unrelated compiler diagnostic from shared stderr\n",
		}},
	}
	findings, err := NewGoTestAnalyzer(runner).Analyze(context.Background(), Input{Repository: repository, Packages: []string{"./..."}})
	if err != nil {
		t.Fatalf("unexpected analysis error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("build failure was lost or duplicated: %+v", findings)
	}
	finding := findings[0]
	if finding.Source != NameGoTest || finding.Severity != review.SeverityHigh {
		t.Errorf("explicit build failure classification changed: %+v", finding)
	}
	if !strings.Contains(finding.Evidence, "unrelated compiler diagnostic") {
		t.Errorf("raw stderr should remain available as context: %+v", finding)
	}
	if finding.Location != (review.Location{}) {
		t.Errorf("fallback stderr was incorrectly attributed to the failed build: %+v", finding.Location)
	}
}
