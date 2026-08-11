package analyzer

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

func TestSelectPackagesFromChangedFiles(t *testing.T) {
	repository := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repository, "internal", "service"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := []review.ChangedFile{
		{NewPath: "main.go", Status: review.FileStatusModified},
		{NewPath: "internal/service/service.go", Status: review.FileStatusAdded},
		{NewPath: "README.md", Status: review.FileStatusModified},
		{OldPath: "removed/missing.go", Status: review.FileStatusDeleted},
	}
	packages, err := SelectPackages(repository, files, ScopeChanged)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".", "./internal/service"}
	if !reflect.DeepEqual(packages, want) {
		t.Fatalf("packages = %v, want %v", packages, want)
	}
}

func TestSelectPackagesUsesAllForModuleChange(t *testing.T) {
	packages, err := SelectPackages(t.TempDir(), []review.ChangedFile{{NewPath: "go.mod"}}, ScopeChanged)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(packages, []string{"./..."}) {
		t.Fatalf("packages = %v", packages)
	}
}

func TestChangedLineSet(t *testing.T) {
	repository := t.TempDir()
	files := []review.ChangedFile{{
		NewPath: "internal/service.go",
		Hunks: []review.Hunk{{Lines: []review.DiffLine{
			{Kind: review.LineContext, NewLine: 9},
			{Kind: review.LineAddition, NewLine: 10},
			{Kind: review.LineAddition, NewLine: 11},
		}}},
	}}
	set := BuildChangedLineSet(repository, files)
	if !set.Contains(review.Location{Path: filepath.Join(repository, "internal", "service.go"), StartLine: 10}) {
		t.Fatal("changed line set does not contain absolute changed path")
	}
	if !set.Contains(review.Location{Path: "internal/service.go", StartLine: 9, EndLine: 10}) {
		t.Fatal("changed line set does not match an intersecting range")
	}
	if set.Contains(review.Location{Path: "internal/service.go", StartLine: 9}) {
		t.Fatal("changed line set contains an unchanged context line")
	}
	if set.Contains(review.Location{Path: "other.go", StartLine: 10}) {
		t.Fatal("changed line set contains another file")
	}
}
