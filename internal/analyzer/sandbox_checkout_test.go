package analyzer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func sandboxFixtureGit(t *testing.T, repository string, arguments ...string) string {
	t.Helper()
	args := []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "commit.gpgsign=false", "-c", "user.name=Aegis Test", "-c", "user.email=aegis@example.invalid"}
	command := exec.Command("git", append(args, arguments...)...)
	command.Dir = repository
	command.Env = sandboxGitEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Git fixture %v failed: %v %s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}

func cleanSandboxFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git required for checkout boundary fixture")
	}
	repository := t.TempDir()
	repository, _ = filepath.EvalSymlinks(repository)
	sandboxFixtureGit(t, repository, "init", "--quiet")
	for name, data := range map[string]string{".gitignore": ".env\n*.log\n", "main.go": "package sample\n"} {
		if err := os.WriteFile(filepath.Join(repository, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	sandboxFixtureGit(t, repository, "add", ".")
	sandboxFixtureGit(t, repository, "commit", "--quiet", "-m", "fixture")
	return repository
}

func TestSandboxCheckoutRejectsPrivateAndDirtySourceMounts(t *testing.T) {
	for _, test := range []struct {
		name, filename, data, want string
		stage                      bool
	}{
		{"ignored key", ".env", "DEEPSEEK_API_KEY=local-private-key\n", "ignored files", false},
		{"untracked key", "private.txt", "local-private-key", "untracked files", false},
		{"dirty tracked file", "main.go", "package changed\n", "modified or staged", false},
		{"staged file", "main.go", "package changed\n", "modified or staged", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := cleanSandboxFixture(t)
			path := filepath.Join(repository, test.filename)
			if err := os.WriteFile(path, []byte(test.data), 0600); err != nil {
				t.Fatal(err)
			}
			if test.stage {
				sandboxFixtureGit(t, repository, "add", test.filename)
			}
			err := validateSandboxCheckout(repository)
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), "disposable") || strings.Contains(err.Error(), "local-private-key") {
				t.Fatalf("unsafe source mount accepted or leaked contents: %v", err)
			}
			if contents, err := os.ReadFile(path); err != nil || string(contents) != test.data {
				t.Fatal("source validation modified user data")
			}
			// The constructor must enforce this before looking for a Docker
			// executable; dirty source is rejected even on machines without it.
			if _, err := NewDockerRunner(repository, "aegis-analysis:local", ""); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("constructor bypassed clean mount validation: %v", err)
			}
		})
	}
}

func TestSandboxCheckoutAcceptsCleanGitRootAndRejectsScopeBypass(t *testing.T) {
	repository := cleanSandboxFixture(t)
	if err := validateSandboxCheckout(repository); err != nil {
		t.Fatal(err)
	}
	subdirectory := filepath.Join(repository, "empty")
	if err := os.Mkdir(subdirectory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := validateSandboxCheckout(subdirectory); err == nil || !strings.Contains(err.Error(), "repository root") {
		t.Fatalf("Git subdirectory accepted: %v", err)
	}
	if err := validateSandboxCheckout(t.TempDir()); err == nil {
		t.Fatal("non-Git source accepted")
	}
	other := cleanSandboxFixture(t)
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	if err := validateSandboxCheckout(repository); err != nil {
		t.Fatalf("inherited Git override affected validation: %v", err)
	}
}

func TestSandboxCheckoutRejectsHiddenIndexModifications(t *testing.T) {
	for _, flag := range []string{"--assume-unchanged", "--skip-worktree"} {
		t.Run(flag, func(t *testing.T) {
			repository := cleanSandboxFixture(t)
			sandboxFixtureGit(t, repository, "update-index", flag, "main.go")
			if err := os.WriteFile(filepath.Join(repository, "main.go"), []byte("package secret\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := validateSandboxCheckout(repository); err == nil {
				t.Fatal("index optimization hid modified private source")
			}
		})
	}
}

func TestSandboxCheckoutRejectsLinkedWorktreeMetadata(t *testing.T) {
	repository := cleanSandboxFixture(t)
	worktree := filepath.Join(t.TempDir(), "linked")
	sandboxFixtureGit(t, repository, "worktree", "add", "--detach", worktree, "HEAD")
	worktree, _ = filepath.EvalSymlinks(worktree)
	if err := validateSandboxCheckout(worktree); err == nil || !strings.Contains(err.Error(), "standalone .git directory") {
		t.Fatalf("worktree Git metadata indirection accepted: %v", err)
	}
}
