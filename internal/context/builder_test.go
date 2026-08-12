package repocontext

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Molly166/AegisCodeAgent/internal/analyzer"
	"github.com/Molly166/AegisCodeAgent/internal/review"
)

type contextRunner struct {
	execution analyzer.Execution
	err       error
	available bool
}

func (r contextRunner) LookPath(string) (string, error) {
	if r.available {
		return "/tools/go", nil
	}
	return "", errors.New("not found")
}

func (r contextRunner) Run(context.Context, analyzer.Command) (analyzer.Execution, error) {
	return r.execution, r.err
}

func TestBuilderFindsChangedSymbolsCallersInterfacesAndTests(t *testing.T) {
	repository, serviceSource := createContextFixture(t)
	packageJSON, err := json.Marshal(goListPackage{
		Dir: repository, ImportPath: "example.com/contextfixture", Name: "contextfixture",
		GoFiles: []string{"service.go"}, TestGoFiles: []string{"service_test.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	builder := NewBuilder(contextRunner{
		available: true,
		execution: analyzer.Execution{Stdout: string(packageJSON)},
	})
	bundle, err := builder.Build(context.Background(), Input{
		Repository: repository,
		Packages:   []string{"./..."},
		Files:      changedServiceFile(serviceSource),
		Budget:     Budget{MaxSymbols: 20, MaxTotalBytes: 64 * 1024},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if bundle.Status != review.ContextComplete {
		t.Fatalf("context status = %q, warnings=%v", bundle.Status, bundle.Warnings)
	}
	if bundle.Stats.PackagesLoaded != 1 || bundle.Stats.FilesParsed != 2 {
		t.Fatalf("unexpected stats: %+v", bundle.Stats)
	}
	if !containsSymbol(bundle.ChangedSymbols, "*Worker.Run") {
		t.Fatalf("changed method not found: %+v", bundle.ChangedSymbols)
	}
	for _, expected := range []string{"validate", "Execute", "TestWorkerRun", "Worker", "Runner"} {
		if !containsSymbol(bundle.RelatedSymbols, expected) {
			t.Errorf("related symbol %q not found: %+v", expected, bundle.RelatedSymbols)
		}
	}
	for _, kind := range []review.RelationKind{review.RelationCalls, review.RelationTests, review.RelationMemberOf} {
		if !containsRelation(bundle.Relations, kind) {
			t.Errorf("relation %q not found: %+v", kind, bundle.Relations)
		}
	}
	if !containsRelation(bundle.Relations, review.RelationImplements) && !containsRelation(bundle.Relations, review.RelationImplementsCandidate) {
		t.Errorf("interface implementation relation not found: %+v", bundle.Relations)
	}
	if bundle.Stats.PackagesTypeChecked != 1 || bundle.Stats.TypeCheckFailures != 0 {
		t.Errorf("unexpected type-check stats: %+v", bundle.Stats)
	}
	if bundle.Stats.EstimatedTokens <= 0 {
		t.Fatalf("estimated tokens = %d", bundle.Stats.EstimatedTokens)
	}
}

func TestBuilderAppliesSymbolBudget(t *testing.T) {
	repository, serviceSource := createContextFixture(t)
	packageJSON, _ := json.Marshal(goListPackage{
		Dir: repository, ImportPath: "example.com/contextfixture", Name: "contextfixture",
		GoFiles: []string{"service.go"}, TestGoFiles: []string{"service_test.go"},
	})
	bundle, err := NewBuilder(contextRunner{
		available: true, execution: analyzer.Execution{Stdout: string(packageJSON)},
	}).Build(context.Background(), Input{
		Repository: repository,
		Packages:   []string{"./..."},
		Files:      changedServiceFile(serviceSource),
		Budget:     Budget{MaxSymbols: 2, MaxTotalBytes: 64 * 1024},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bundle.Truncated || bundle.Stats.SymbolsSelected != 2 {
		t.Fatalf("unexpected budget result: truncated=%v stats=%+v", bundle.Truncated, bundle.Stats)
	}
	if len(bundle.ChangedSymbols) != 1 {
		t.Fatalf("changed symbol was not prioritized: %+v", bundle.ChangedSymbols)
	}
}

func TestBuilderUsesRealGoList(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not installed")
	}
	repository, serviceSource := createContextFixture(t)
	bundle, err := NewBuilder(analyzer.OSRunner{}).Build(context.Background(), Input{
		Repository: repository,
		Packages:   []string{"./..."},
		Files:      changedServiceFile(serviceSource),
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if !containsSymbol(bundle.ChangedSymbols, "*Worker.Run") || bundle.Stats.FilesParsed != 2 {
		t.Fatalf("unexpected bundle: %+v", bundle)
	}
}

func TestBuilderCanonicalizesRepositorySymlink(t *testing.T) {
	repository, serviceSource := createContextFixture(t)
	alias := filepath.Join(t.TempDir(), "repository-link")
	if err := os.Symlink(repository, alias); err != nil {
		t.Skipf("create repository symlink: %v", err)
	}
	packageJSON, _ := json.Marshal(goListPackage{
		Dir: repository, ImportPath: "example.com/contextfixture", Name: "contextfixture",
		GoFiles: []string{"service.go"}, TestGoFiles: []string{"service_test.go"},
	})
	bundle, err := NewBuilder(contextRunner{
		available: true, execution: analyzer.Execution{Stdout: string(packageJSON)},
	}).Build(context.Background(), Input{
		Repository: alias,
		Packages:   []string{"./..."},
		Files:      changedServiceFile(serviceSource),
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if !containsSymbol(bundle.ChangedSymbols, "*Worker.Run") {
		t.Fatalf("changed method not found through repository symlink: %+v", bundle.ChangedSymbols)
	}
}

func TestBuilderReportsGoListFailure(t *testing.T) {
	_, err := NewBuilder(contextRunner{
		available: true,
		execution: analyzer.Execution{ExitCode: 1, Stderr: "module load failed"},
	}).Build(context.Background(), Input{Repository: t.TempDir(), Packages: []string{"./..."}})
	if err == nil || !strings.Contains(err.Error(), "module load failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuilderMarksTypeFallbackPartial(t *testing.T) {
	repository := t.TempDir()
	writeContextFile(t, filepath.Join(repository, "bad.go"), "package bad\n\nimport _ \"example.com/missing\"\n")
	packageJSON, _ := json.Marshal(goListPackage{
		Dir: repository, ImportPath: "example.com/bad", Name: "bad", GoFiles: []string{"bad.go"},
	})
	bundle, err := NewBuilder(contextRunner{
		available: true, execution: analyzer.Execution{Stdout: string(packageJSON)},
	}).Build(context.Background(), Input{Repository: repository, Packages: []string{"."}})
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Status != review.ContextPartial || bundle.Stats.TypeCheckFailures != 1 {
		t.Fatalf("unexpected fallback status: %+v", bundle)
	}
	if len(bundle.Warnings) == 0 || !strings.Contains(bundle.Warnings[0], "AST fallback") {
		t.Fatalf("fallback warning not reported: %v", bundle.Warnings)
	}
}

func createContextFixture(t *testing.T) (string, string) {
	t.Helper()
	repository := t.TempDir()
	serviceSource := `package contextfixture

type Runner interface {
	Run(string) error
}

type Worker struct{}

func (worker *Worker) Run(task string) error {
	return validate(task)
}

func validate(task string) error {
	return nil
}

func Execute(worker *Worker, task string) error {
	return worker.Run(task)
}
`
	testSource := `package contextfixture

import "testing"

func TestWorkerRun(t *testing.T) {
	worker := &Worker{}
	if err := worker.Run("task"); err != nil {
		t.Fatal(err)
	}
}
`
	writeContextFile(t, filepath.Join(repository, "go.mod"), "module example.com/contextfixture\n\ngo 1.23\n")
	writeContextFile(t, filepath.Join(repository, "service.go"), serviceSource)
	writeContextFile(t, filepath.Join(repository, "service_test.go"), testSource)
	return repository, serviceSource
}

func changedServiceFile(source string) []review.ChangedFile {
	line := lineContaining(source, "func (worker *Worker) Run")
	return []review.ChangedFile{{
		NewPath: "service.go",
		Status:  review.FileStatusModified,
		Hunks: []review.Hunk{{
			NewStart: line,
			NewLines: 3,
			Lines:    []review.DiffLine{{Kind: review.LineAddition, NewLine: line + 1}},
		}},
	}}
}

func lineContaining(source, value string) int {
	for index, line := range strings.Split(source, "\n") {
		if strings.Contains(line, value) {
			return index + 1
		}
	}
	return 0
}

func containsSymbol(symbols []review.ContextSymbol, qualifiedName string) bool {
	for _, symbol := range symbols {
		if symbol.QualifiedName == qualifiedName {
			return true
		}
	}
	return false
}

func containsRelation(relations []review.ContextRelation, kind review.RelationKind) bool {
	for _, relation := range relations {
		if relation.Kind == kind {
			return true
		}
	}
	return false
}

func writeContextFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
