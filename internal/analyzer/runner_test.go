package analyzer

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLimitedBufferTruncatesWithoutShortWrite(t *testing.T) {
	buffer := newLimitedBuffer(5)
	input := []byte("123456789")
	written, err := buffer.Write(input)
	if err != nil || written != len(input) {
		t.Fatalf("Write() = %d, %v", written, err)
	}
	if !buffer.Truncated() || !strings.Contains(buffer.String(), "12345") || !strings.Contains(buffer.String(), "truncated") {
		t.Fatalf("unexpected buffer: %q", buffer.String())
	}
}

func TestOSRunnerDoesNotExposeCredentialsToChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-specific")
	}
	directory := t.TempDir()
	script := filepath.Join(directory, "inspect-env.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s|%s' \"$DEEPSEEK_API_KEY\" \"$GOCACHE\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEEPSEEK_API_KEY", "must-not-reach-child")
	t.Setenv("GOCACHE", "/tmp/aegis-test-cache")
	execution, err := (OSRunner{}).Run(context.Background(), Command{Name: script, Directory: directory})
	if err != nil {
		t.Fatal(err)
	}
	if execution.Stdout != "|/tmp/aegis-test-cache" {
		t.Fatalf("unexpected child environment: %q", execution.Stdout)
	}
}
