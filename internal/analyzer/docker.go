package analyzer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DockerRunner executes repository-controlled compilers and tests outside the
// credential-bearing reviewer process. It never falls back to host execution.
// Cache is an isolated, disposable build directory; it is not a shared host cache.
type DockerRunner struct {
	Repository     string
	Image          string
	Cache          string
	ModuleCache    string
	MaxOutputBytes int
	docker         string
}

var sandboxImagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,255}$`)

func NewDockerRunner(repository, image, moduleCache string) (*DockerRunner, error) {
	root, err := sandboxDirectory(repository)
	if err != nil {
		return nil, fmt.Errorf("sandbox repository: %w", err)
	}
	if err := validateSandboxCheckout(root); err != nil {
		return nil, err
	}
	if !sandboxImagePattern.MatchString(image) {
		return nil, errors.New("sandbox image must be an explicit Docker image reference")
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		return nil, errors.New("docker is required for --sandbox=docker; host execution is not a fallback")
	}
	if moduleCache != "" {
		moduleCache, err = sandboxDirectory(moduleCache)
		if err != nil {
			return nil, fmt.Errorf("sandbox module cache: %w", err)
		}
		if containsDirectory(root, moduleCache) || containsDirectory(moduleCache, root) {
			return nil, errors.New("sandbox module cache must be separate from the reviewed repository")
		}
	}
	cache, err := os.MkdirTemp("", "aegis-build-cache-")
	if err != nil {
		return nil, err
	}
	return &DockerRunner{Repository: root, Image: image, Cache: cache, ModuleCache: moduleCache, MaxOutputBytes: 16 * 1024 * 1024, docker: docker}, nil
}

// Close only removes the directory allocated by this runner, never caller paths.
func (r *DockerRunner) Close() error {
	if r.Cache == "" || !strings.HasPrefix(filepath.Base(r.Cache), "aegis-build-cache-") {
		return errors.New("invalid sandbox cache cleanup target")
	}
	return os.RemoveAll(r.Cache)
}

func (r *DockerRunner) ExportRoots() []string { return []string{r.Cache} }

func (r *DockerRunner) LookPath(name string) (string, error) {
	switch name {
	case "go", "staticcheck", "gosec":
		return name, nil
	default:
		return "", fmt.Errorf("%q is not a sandbox analysis tool", name)
	}
}

func (r *DockerRunner) Run(ctx context.Context, command Command) (Execution, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Execution{}, err
	}
	name := "aegis-" + hex.EncodeToString(nonce[:])
	args, err := r.arguments(command, name)
	if err != nil {
		return Execution{}, err
	}
	limit := r.MaxOutputBytes
	if limit <= 0 {
		limit = defaultOutputLimit
	}
	stdout, stderr := newLimitedBuffer(limit), newLimitedBuffer(limit)
	process := exec.CommandContext(ctx, r.docker, args...)
	process.Stdout, process.Stderr = stdout, stderr
	// The Docker client needs its host connection settings, but none are passed
	// into the container: all container environment entries are literal below.
	process.WaitDelay = 2 * time.Second
	started := time.Now()
	err = process.Run()
	if ctx.Err() != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Killing the Docker CLI does not kill its container. Remove this exact
		// cryptographically unique name, including all test descendants.
		_ = exec.CommandContext(cleanupCtx, r.docker, "rm", "--force", name).Run()
	}
	result := Execution{Stdout: stdout.String(), Stderr: stderr.String(), Duration: time.Since(started), Truncated: stdout.Truncated() || stderr.Truncated()}
	if err == nil {
		return result, executionError(result, nil)
	}
	if ctx.Err() != nil {
		return result, executionError(result, ctx.Err())
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, executionError(result, nil)
	}
	return result, executionError(result, fmt.Errorf("start sandbox: %w", err))
}

func (r *DockerRunner) arguments(command Command, name string) ([]string, error) {
	if _, err := r.LookPath(command.Name); err != nil {
		return nil, err
	}
	directory, err := sandboxDirectory(command.Directory)
	if err != nil || !containsDirectory(r.Repository, directory) {
		return nil, errors.New("sandbox command directory escapes repository")
	}
	for _, path := range []string{r.Repository, r.Cache, r.ModuleCache} {
		if strings.ContainsAny(path, ",:\n\r") {
			return nil, errors.New("sandbox mount paths cannot contain commas, colons, or newlines")
		}
	}
	args := []string{"run", "--rm", "--pull=never", "--name", name,
		"--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges",
		"--pids-limit=256", "--memory=2g", "--cpus=2", "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"--tmpfs", "/tmp:rw,nosuid,nodev,size=1g", "--workdir", directory,
		// Even a clean clone's Git metadata may contain authenticated remote
		// URLs or extraheaders. Shadow it entirely with an empty read-only mount.
		"--tmpfs", filepath.Join(r.Repository, ".git") + ":ro,nosuid,nodev,noexec,size=1m",
		"--mount", "type=bind,src=" + r.Repository + ",dst=" + r.Repository + ",readonly",
		"--mount", "type=bind,src=" + r.Cache + ",dst=" + r.Cache,
		"--env", "HOME=/tmp", "--env", "GOCACHE=" + r.Cache,
		"--env", "GOMODCACHE=/go/pkg/mod", "--env", "GOPATH=/go", "--env", "GOTOOLCHAIN=local",
		"--env", "GOPROXY=off", "--env", "GOSUMDB=off", "--env", "GOWORK=off",
		"--env", "GOFLAGS=-mod=readonly -buildvcs=false", "--env", "CGO_ENABLED=0"}
	if r.ModuleCache != "" {
		args = append(args, "--mount", "type=bind,src="+r.ModuleCache+",dst=/go/pkg/mod,readonly")
	}
	args = append(args, "--entrypoint", command.Name, r.Image)
	return append(args, command.Arguments...), nil
}

func sandboxDirectory(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("directory is required")
	}
	root, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", errors.New("path must be an existing directory")
	}
	if root == string(filepath.Separator) {
		return "", errors.New("filesystem root is not an analysis directory")
	}
	return root, nil
}

func containsDirectory(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
