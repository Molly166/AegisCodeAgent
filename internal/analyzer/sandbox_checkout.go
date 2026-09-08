package analyzer

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Molly166/AegisCodeAgent/internal/secureenv"
)

// The source bind mount is deliberately literal. An ignored local .env would
// be readable by PR tests even without inherited environment credentials. Only
// accept a disposable clean Git checkout; never delete or copy user data here.
func validateSandboxCheckout(repository string) error {
	metadata, metadataErr := os.Lstat(filepath.Join(repository, ".git"))
	if metadataErr != nil || !metadata.IsDir() || metadata.Mode()&os.ModeSymlink != 0 {
		return errors.New("docker analysis requires a standalone .git directory, not worktree or symlink indirection; use a disposable fresh clone at the repository root")
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return errors.New("docker analysis requires Git and a disposable clean checkout")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	inspect := func(arguments ...string) (string, error) {
		args := []string{"--no-optional-locks", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "core.hooksPath=" + os.DevNull}
		args = append(args, arguments...)
		command := exec.CommandContext(ctx, git, args...)
		command.Dir = repository
		command.Env = sandboxGitEnvironment()
		stdout, stderr := newLimitedBuffer(1024*1024), newLimitedBuffer(1024*1024)
		command.Stdout, command.Stderr = stdout, stderr
		command.WaitDelay = 2 * time.Second
		if err := command.Run(); err != nil || stdout.Truncated() || stderr.Truncated() {
			return "", errors.New("docker analysis could not verify checkout cleanliness; use a disposable fresh Git checkout")
		}
		return stdout.String(), nil
	}
	top, err := inspect("rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(strings.TrimSpace(top))
	if err != nil || resolved != repository {
		return errors.New("docker analysis requires the Git repository root, not a subdirectory; use a disposable fresh checkout")
	}
	if _, err := inspect("rev-parse", "--verify", "HEAD"); err != nil {
		return err
	}
	for _, check := range []struct {
		arguments []string
		label     string
	}{
		{[]string{"status", "--porcelain=v1", "--untracked-files=no", "--ignore-submodules=none", "-z"}, "modified or staged source files"},
		{[]string{"ls-files", "--others", "--exclude-standard", "-z"}, "untracked files"},
		{[]string{"ls-files", "--others", "--ignored", "--exclude-standard", "-z"}, "ignored files (which may contain local credentials)"},
	} {
		output, err := inspect(check.arguments...)
		if err != nil {
			return err
		}
		if output != "" {
			return errors.New("docker source mount contains " + check.label + "; use a disposable fresh checkout without local private files")
		}
	}
	// status intentionally trusts assume-unchanged/skip-worktree bits. Refuse
	// such an index so these local optimization flags cannot hide dirty content.
	entries, err := inspect("ls-files", "-v", "-z")
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(entries, "\x00") {
		if entry == "" {
			continue
		}
		if entry[0] == 'S' || entry[0] >= 'a' && entry[0] <= 'z' {
			return errors.New("docker analysis cannot trust assume-unchanged or skip-worktree index entries; use a disposable fresh checkout")
		}
	}
	// Git does not enumerate ignored files inside initialized submodules when
	// inspecting the parent index. Those nested worktrees need separate review.
	staged, err := inspect("ls-files", "--stage", "-z")
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(staged, "\x00") {
		if !strings.HasPrefix(entry, "160000 ") {
			continue
		}
		_, path, ok := strings.Cut(entry, "\t")
		if !ok {
			return errors.New("docker analysis encountered an invalid submodule entry")
		}
		children, readErr := os.ReadDir(filepath.Join(repository, filepath.FromSlash(path)))
		if readErr == nil && len(children) > 0 {
			return errors.New("docker source mount contains an initialized submodule whose private files cannot be verified; use a disposable checkout without initialized submodules")
		}
	}
	return nil
}

func sandboxGitEnvironment() []string {
	environment := make([]string, 0)
	for _, entry := range secureenv.ForUntrustedChild(os.Environ()) {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(name), "GIT_") {
			environment = append(environment, entry)
		}
	}
	return append(environment, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
}
