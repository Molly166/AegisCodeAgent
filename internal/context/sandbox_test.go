package repocontext

import (
	"context"
	"encoding/json"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Molly166/AegisCodeAgent/internal/analyzer"
	"github.com/Molly166/AegisCodeAgent/internal/review"
)

func TestIndexDoesNotReadSymlinkOutsideRepository(t *testing.T) {
	repository, outside := t.TempDir(), t.TempDir()
	external := filepath.Join(outside, "private.go")
	if err := os.WriteFile(external, []byte("package p\nconst Private = 123\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(repository, "main.go")); err != nil {
		t.Skip(err)
	}
	index := newRepositoryIndex(repository, normalizeBudget(Budget{}), nil)
	index.addPackage(goListPackage{Dir: repository, ImportPath: "sample", Name: "p", GoFiles: []string{"main.go"}})
	if index.filesParsed != 0 || len(index.warnings) == 0 {
		t.Fatalf("read source outside repository: %+v", index.warnings)
	}
	if exportWithinRoots(external, []string{repository}) {
		t.Fatal("allowed external compiler export")
	}
}

func TestIndexDoesNotFollowSymlinkIntoHiddenSource(t *testing.T) {
	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, ".private.go"), []byte("package p\nconst PrivateKey = 123\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".private.go", filepath.Join(repository, "main.go")); err != nil {
		t.Skip(err)
	}
	index := newRepositoryIndex(repository, normalizeBudget(Budget{}), nil)
	index.addPackage(goListPackage{Dir: repository, ImportPath: "sample", Name: "p", GoFiles: []string{"main.go"}})
	if index.filesParsed != 0 || len(index.warnings) == 0 {
		t.Fatalf("read hidden source through an alias: %+v", index.warnings)
	}
}

type panickingImporter struct{}

func (panickingImporter) Import(string) (*types.Package, error) {
	panic("unsupported compiler export format")
}

func TestIncompatibleExportDataDegradesWithoutCrashing(t *testing.T) {
	i := &exportImporter{compiler: panickingImporter{}, exports: map[string]string{"example": "export.a"}}
	pkg, err := i.Import("example")
	if pkg != nil || err == nil || !strings.Contains(err.Error(), "align reviewer and analyzer toolchains") {
		t.Fatalf("expected explicit type-context degradation, got %v, %v", pkg, err)
	}
}

type truncatedExportRunner struct{ packageJSON string }

func (truncatedExportRunner) LookPath(string) (string, error) { return "go", nil }
func (r truncatedExportRunner) Run(_ context.Context, command analyzer.Command) (analyzer.Execution, error) {
	if strings.Contains(strings.Join(command.Arguments, " "), "-export") {
		return analyzer.Execution{Truncated: true}, nil
	}
	return analyzer.Execution{Stdout: r.packageJSON}, nil
}

func TestTruncatedExportMetadataCannotClaimCompleteContext(t *testing.T) {
	repository, source := createContextFixture(t)
	data, err := json.Marshal(goListPackage{Dir: repository, ImportPath: "example.com/contextfixture", Name: "contextfixture", GoFiles: []string{"service.go"}})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := NewBuilder(truncatedExportRunner{string(data)}).Build(context.Background(), Input{Repository: repository, Packages: []string{"./..."}, Files: changedServiceFile(source)})
	if err != nil || bundle.Status != review.ContextPartial || !strings.Contains(strings.Join(bundle.Warnings, " "), "truncated") {
		t.Fatalf("truncation was hidden: status=%s warnings=%v err=%v", bundle.Status, bundle.Warnings, err)
	}
	if len(bundle.ChangedSymbols) == 0 {
		t.Fatal("AST fallback was unnecessarily discarded")
	}
}
