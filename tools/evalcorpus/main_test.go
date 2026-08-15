package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	evalpkg "github.com/Molly166/AegisCodeAgent/internal/eval"
	"github.com/Molly166/AegisCodeAgent/internal/githubreport"
)

func TestCheckedInCatalogGeneratesValidDeterministicCorpus(t *testing.T) {
	definition, err := loadCatalog(filepath.Join("..", "..", "eval", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	output := t.TempDir()
	if err := generate(definition, output); err != nil {
		t.Fatal(err)
	}
	first := snapshot(t, output)
	if len(first) != 100 {
		t.Fatalf("generated corpus must contain 50 case/report pairs, got %d files", len(first))
	}

	report, err := evalpkg.Run(evalpkg.Config{Corpus: output, Gate: githubreport.Options{
		FailOn: githubreport.PriorityP1, FailOnNeedsReview: githubreport.PriorityP0, FailOnIncomplete: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Metrics.Cases != 50 || report.Metrics.PassedCases != 50 {
		t.Fatalf("generated corpus is invalid: %+v", report.Metrics)
	}

	if err := generate(definition, output); err != nil {
		t.Fatal(err)
	}
	second := snapshot(t, output)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("corpus generation is not deterministic")
	}
}

func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path) // #nosec G304 -- path is yielded by WalkDir beneath t.TempDir
		if err != nil {
			return err
		}
		digest := sha256.Sum256(content)
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = hex.EncodeToString(digest[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
