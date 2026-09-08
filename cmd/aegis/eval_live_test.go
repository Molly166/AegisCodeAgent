package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestEvalLiveHelpAndInvalidArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runEvalLive(context.Background(), []string{"--help"}, &stdout, &stderr); code != 0 || !strings.Contains(stderr.String(), "synthetic") {
		t.Fatalf("help: code=%d output=%s", code, stderr.String())
	}
	for _, args := range [][]string{{"extra"}, {"--timeout=0"}, {"--format=invalid"}, {"--fail-on=p7"}} {
		stderr.Reset()
		if code := runEvalLive(context.Background(), args, &stdout, &stderr); code != 2 {
			t.Errorf("invalid args %v returned %d", args, code)
		}
	}
}
