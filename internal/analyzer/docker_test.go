package analyzer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerRunnerBoundary(t *testing.T) {
	repository, cache, modules := t.TempDir(), t.TempDir(), t.TempDir()
	repository, _ = filepath.EvalSymlinks(repository)
	r := &DockerRunner{Repository: repository, Cache: cache, ModuleCache: modules, Image: "aegis-analysis:local"}
	t.Setenv("ORCAROUTER_API_KEY", "secret-never-pass")
	args, err := r.arguments(Command{Name: "go", Arguments: []string{"test", "./..."}, Directory: repository}, "aegis-test")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, required := range []string{"--network=none", "--read-only", "--pull=never", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit=256", "--entrypoint go", "GOPROXY=off", "CGO_ENABLED=0", "GOFLAGS=-mod=readonly -buildvcs=false", "--tmpfs " + filepath.Join(repository, ".git") + ":ro,nosuid,nodev,noexec,size=1m", "src=" + repository + ",dst=" + repository + ",readonly", "src=" + modules + ",dst=/go/pkg/mod,readonly"} {
		if !strings.Contains(joined, required) {
			t.Errorf("sandbox missing %s", required)
		}
	}
	for _, forbidden := range []string{"secret-never-pass", "ORCAROUTER_API_KEY", "docker.sock", "--privileged", "--pid=host", "--network=host"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("unsafe sandbox argument %s", forbidden)
		}
	}
	for _, command := range []Command{{Name: "sh", Directory: repository}, {Name: "go", Directory: filepath.Dir(repository)}} {
		if _, err := r.arguments(command, "aegis-test"); err == nil {
			t.Errorf("accepted unsafe command %+v", command)
		}
	}
	r.Cache = cache + ":injected"
	if _, err := r.arguments(Command{Name: "go", Directory: repository}, "aegis-test"); err == nil {
		t.Fatal("colon in tmpfs/bind mount path was accepted")
	}
}

func TestDockerRunnerTemporaryExecutionBoundary(t *testing.T) {
	repository := t.TempDir()
	repository, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	r := &DockerRunner{Repository: repository, Cache: t.TempDir(), Image: "aegis-analysis:local"}
	args, err := r.arguments(Command{Name: "go", Arguments: []string{"test", "./..."}, Directory: repository}, "aegis-test")
	if err != nil {
		t.Fatal(err)
	}
	tmpfs := make(map[string]string)
	var goTempDirs []string
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--tmpfs":
			path, options, ok := strings.Cut(args[i+1], ":")
			if !ok || tmpfs[path] != "" {
				t.Fatalf("invalid or duplicate tmpfs mount: %q", args[i+1])
			}
			tmpfs[path] = options
		case "--env":
			if strings.HasPrefix(args[i+1], "GOTMPDIR=") {
				goTempDirs = append(goTempDirs, args[i+1])
			}
		}
	}
	if len(tmpfs) != 3 {
		t.Fatalf("unexpected temporary mounts: %v", tmpfs)
	}
	for path, want := range map[string]string{
		"/tmp":                            "rw,noexec,nosuid,nodev,size=1g",
		"/aegis-tmp":                      fmt.Sprintf("rw,exec,nosuid,nodev,size=1g,mode=0700,uid=%d,gid=%d", os.Getuid(), os.Getgid()),
		filepath.Join(repository, ".git"): "ro,nosuid,nodev,noexec,size=1m",
	} {
		if got := tmpfs[path]; got != want {
			t.Errorf("tmpfs %s: got %q, want %q", path, got, want)
		}
	}
	if len(goTempDirs) != 1 || goTempDirs[0] != "GOTMPDIR=/aegis-tmp" {
		t.Errorf("Go test executables must use the dedicated temporary mount: %v", goTempDirs)
	}
}
