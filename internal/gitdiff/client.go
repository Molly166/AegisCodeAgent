package gitdiff

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

const defaultTimeout = 30 * time.Second

type Options struct {
	Repository   string
	Base         string
	Head         string
	ContextLines int
}

type Result struct {
	Repository     string
	Base           string
	Head           string
	BaseCommit     string
	HeadCommit     string
	WorktreeCommit string
	WorktreeClean  bool
	Files          []review.ChangedFile
}

type Client struct {
	timeout time.Duration
}

func NewClient(timeout time.Duration) Client {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return Client{timeout: timeout}
}

func (c Client) Collect(ctx context.Context, options Options) (Result, error) {
	if strings.TrimSpace(options.Repository) == "" {
		return Result{}, errors.New("repository path is required")
	}
	if strings.TrimSpace(options.Base) == "" {
		return Result{}, errors.New("base revision is required")
	}
	if strings.TrimSpace(options.Head) == "" {
		return Result{}, errors.New("head revision is required")
	}
	if options.ContextLines < 0 || options.ContextLines > 1000 {
		return Result{}, fmt.Errorf("context lines must be between 0 and 1000, got %d", options.ContextLines)
	}

	operationContext, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	repository, err := resolveRepository(operationContext, options.Repository)
	if err != nil {
		return Result{}, err
	}
	baseCommit, err := resolveRevision(operationContext, repository, options.Base)
	if err != nil {
		return Result{}, fmt.Errorf("resolve base revision %q: %w", options.Base, err)
	}
	headCommit, err := resolveRevision(operationContext, repository, options.Head)
	if err != nil {
		return Result{}, fmt.Errorf("resolve head revision %q: %w", options.Head, err)
	}
	worktreeCommit, err := resolveRevision(operationContext, repository, "HEAD")
	if err != nil {
		return Result{}, fmt.Errorf("resolve checked-out HEAD: %w", err)
	}
	statusOutput, err := runGit(operationContext, repository, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return Result{}, fmt.Errorf("inspect worktree status: %w", err)
	}

	output, err := runGit(operationContext, repository,
		"-c", "core.quotePath=true",
		"diff", "--no-color", "--no-ext-diff", "--find-renames",
		"--unified="+strconv.Itoa(options.ContextLines),
		baseCommit+"..."+headCommit, "--",
	)
	if err != nil {
		return Result{}, fmt.Errorf("generate git diff: %w", err)
	}
	files, err := Parse(bytes.NewReader(output))
	if err != nil {
		return Result{}, fmt.Errorf("parse git diff: %w", err)
	}
	return Result{
		Repository:     repository,
		Base:           options.Base,
		Head:           options.Head,
		BaseCommit:     baseCommit,
		HeadCommit:     headCommit,
		WorktreeCommit: worktreeCommit,
		WorktreeClean:  strings.TrimSpace(string(statusOutput)) == "",
		Files:          files,
	}, nil
}

func resolveRepository(ctx context.Context, repository string) (string, error) {
	absolutePath, err := filepath.Abs(repository)
	if err != nil {
		return "", fmt.Errorf("resolve repository path: %w", err)
	}
	info, err := os.Stat(absolutePath)
	if err != nil {
		return "", fmt.Errorf("inspect repository path: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("repository path %q is not a directory", absolutePath)
	}
	output, err := runGit(ctx, absolutePath, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("locate git repository: %w", err)
	}
	return filepath.Clean(strings.TrimSpace(string(output))), nil
}

func resolveRevision(ctx context.Context, repository, revision string) (string, error) {
	output, err := runGit(ctx, repository, "rev-parse", "--verify", "--end-of-options", revision+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func runGit(ctx context.Context, repository string, arguments ...string) ([]byte, error) {
	commandArguments := append([]string{"-C", repository}, arguments...)
	command := exec.CommandContext(ctx, "git", commandArguments...)
	output, err := command.CombinedOutput()
	if err == nil {
		return output, nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("git command timed out: %w", ctx.Err())
	}
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		detail = err.Error()
	}
	return nil, fmt.Errorf("git %s: %s", strings.Join(arguments, " "), detail)
}
