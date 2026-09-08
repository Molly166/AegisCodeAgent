package analyzer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Explicitly enabled by Linux CI after building the trusted analysis image.
// These assertions execute inside the actual container, not a mocked command.
func TestDockerIntegration(t *testing.T) {
	image := os.Getenv("AEGIS_DOCKER_TEST_IMAGE")
	if image == "" || runtime.GOOS != "linux" {
		t.Skip("set AEGIS_DOCKER_TEST_IMAGE on Linux to exercise Docker isolation")
	}
	repository := t.TempDir()
	outside := filepath.Join(t.TempDir(), "host-private.txt")
	if err := os.WriteFile(outside, []byte("host-file-canary"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AEGIS_DOCKER_TEST_SECRET", "aegis-host-env-canary")
	source := fmt.Sprintf(`package isolated
import ("context"; "errors"; "os"; "os/exec"; "path/filepath"; "net"; "strings"; "testing"; "time")
func TestBoundary(t *testing.T) {
 if os.Getenv("AEGIS_DOCKER_TEST_SECRET") != "" { t.Fatal("host secret visible") }
 if data, _ := os.ReadFile("/proc/1/environ"); strings.Contains(string(data), "aegis-host-env-canary") { t.Fatal("host environment visible through proc") }
 if _, err := os.ReadFile(%q); err == nil { t.Fatal("host private file visible") }
 if _, err := os.ReadFile(".git/config"); err == nil { t.Fatal("Git metadata credentials visible") }
 if err := os.WriteFile(".git/metadata-write", []byte("x"), 0600); err == nil { t.Fatal("shadow Git metadata is writable") }
 if err := os.WriteFile("injected.go", []byte("package isolated"), 0600); err == nil { t.Fatal("source mount is writable") }
 if _, err := os.Stat("/var/run/docker.sock"); err == nil { t.Fatal("docker socket is visible") }
 ifaces, err := net.Interfaces(); if err != nil { t.Fatal(err) }
 for _, iface := range ifaces { if iface.Flags & net.FlagLoopback == 0 { t.Fatal("non-loopback network interface visible") } }
}
func TestTemporaryExecutionBoundary(t *testing.T) {
 if os.Getenv("GOTMPDIR") != "/aegis-tmp" { t.Fatal("Go test temporary directory is not isolated") }
 executable, err := os.Executable(); if err != nil { t.Fatal(err) }
 if !strings.HasPrefix(executable, "/aegis-tmp/") { t.Fatal("Go test binary is outside the dedicated executable mount") }
 data, err := os.ReadFile(executable); if err != nil { t.Fatal(err) }
 for _, directory := range []string{"/tmp", "/aegis-tmp"} {
  path := filepath.Join(directory, "execution-probe.test")
  if err := os.WriteFile(path, data, 0700); err != nil { t.Fatal(err) }
  ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
  output, err := exec.CommandContext(ctx, path, "-test.run=^TestExecutableProbe$").CombinedOutput()
  cancel()
  if directory == "/tmp" {
   if !errors.Is(err, os.ErrPermission) { t.Fatal("general temporary directory must refuse execution", err, string(output)) }
  } else if err != nil || !strings.Contains(string(output), "PASS") {
   t.Fatal("dedicated Go temporary directory must allow execution", err, string(output))
  }
 }
}
func TestExecutableProbe(t *testing.T) {}
`, outside)
	for name, data := range map[string]string{"go.mod": "module isolated\n\ngo 1.23.0\n", "isolation_test.go": source} {
		if err := os.WriteFile(filepath.Join(repository, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	sandboxFixtureGit(t, repository, "init", "--quiet")
	sandboxFixtureGit(t, repository, "add", ".")
	sandboxFixtureGit(t, repository, "commit", "--quiet", "-m", "isolated test fixture")
	sandboxFixtureGit(t, repository, "config", "aegis.test-token", "private-git-metadata-canary")
	runner, err := NewDockerRunner(repository, image, "")
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	result, err := runner.Run(ctx, Command{Name: "go", Arguments: []string{"test", "-v", "./..."}, Directory: repository})
	if err != nil || result.ExitCode != 0 || !strings.Contains(result.Stdout, "PASS") {
		t.Fatalf("container boundary test failed: %v exit=%d\n%s", err, result.ExitCode, result.CombinedOutput())
	}
	runner.MaxOutputBytes = 8
	result, err = runner.Run(ctx, Command{Name: "go", Arguments: []string{"version"}, Directory: repository})
	if !errors.Is(err, ErrOutputTruncated) || !result.Truncated {
		t.Fatalf("container output limit did not mark analysis incomplete: %+v %v", result, err)
	}
}
