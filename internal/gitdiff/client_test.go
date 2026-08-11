package gitdiff

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

func TestClientCollectFromRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repository := t.TempDir()
	runTestGit(t, repository, "init", "-b", "main")
	runTestGit(t, repository, "config", "user.name", "Aegis Test")
	runTestGit(t, repository, "config", "user.email", "aegis@example.com")

	path := filepath.Join(repository, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repository, "add", "main.go")
	runTestGit(t, repository, "commit", "-m", "base")
	base := strings.TrimSpace(runTestGit(t, repository, "rev-parse", "HEAD"))

	if err := os.WriteFile(path, []byte("package main\n\nfunc value() int { return 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repository, "add", "main.go")
	runTestGit(t, repository, "commit", "-m", "head")

	result, err := NewClient(5*time.Second).Collect(context.Background(), Options{
		Repository:   repository,
		Base:         base,
		Head:         "HEAD",
		ContextLines: 3,
	})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if result.BaseCommit != base || result.HeadCommit == "" {
		t.Fatalf("unexpected commits: base=%q head=%q", result.BaseCommit, result.HeadCommit)
	}
	if result.WorktreeCommit != result.HeadCommit || !result.WorktreeClean {
		t.Fatalf("unexpected worktree state: commit=%q clean=%v", result.WorktreeCommit, result.WorktreeClean)
	}
	if len(result.Files) != 1 {
		t.Fatalf("file count = %d, want 1", len(result.Files))
	}
	file := result.Files[0]
	if file.Path() != "main.go" || file.Status != review.FileStatusModified {
		t.Fatalf("unexpected file: %+v", file)
	}
	if file.Stats.Additions != 1 || file.Stats.Deletions != 1 {
		t.Fatalf("unexpected stats: %+v", file.Stats)
	}
}

func TestClientValidatesOptions(t *testing.T) {
	client := NewClient(time.Second)
	if _, err := client.Collect(context.Background(), Options{}); err == nil {
		t.Fatal("Collect() error = nil, want validation error")
	}
	if _, err := client.Collect(context.Background(), Options{
		Repository: ".", Base: "main", Head: "HEAD", ContextLines: -1,
	}); err == nil {
		t.Fatal("Collect() error = nil, want context validation error")
	}
}

func runTestGit(t *testing.T, repository string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return string(output)
}
