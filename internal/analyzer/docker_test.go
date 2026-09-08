package analyzer

import (
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
