package analyzer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/Molly166/AegisCodeAgent/internal/secureenv"
)

const defaultOutputLimit = 2 * 1024 * 1024

type OSRunner struct {
	MaxOutputBytes int
}

func (r OSRunner) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

func (r OSRunner) Run(ctx context.Context, command Command) (Execution, error) {
	if command.Name == "" {
		return Execution{}, errors.New("command name is required")
	}
	if command.Directory == "" {
		return Execution{}, errors.New("command directory is required")
	}
	limit := r.MaxOutputBytes
	if limit <= 0 {
		limit = defaultOutputLimit
	}

	stdout := newLimitedBuffer(limit)
	stderr := newLimitedBuffer(limit)
	process := exec.CommandContext(ctx, command.Name, command.Arguments...)
	process.Dir = command.Directory
	process.Env = secureenv.ForUntrustedChild(os.Environ())
	process.Stdout = stdout
	process.Stderr = stderr

	started := time.Now()
	err := process.Run()
	execution := Execution{
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		ExitCode:  0,
		Duration:  time.Since(started),
		Truncated: stdout.Truncated() || stderr.Truncated(),
	}
	if err == nil {
		return execution, executionError(execution, nil)
	}
	if ctx.Err() != nil {
		return execution, executionError(execution, ctx.Err())
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		execution.ExitCode = exitError.ExitCode()
		return execution, executionError(execution, nil)
	}
	return execution, executionError(execution, fmt.Errorf("start %s: %w", command.Name, err))
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func newLimitedBuffer(limit int) *limitedBuffer {
	return &limitedBuffer{limit: limit}
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	originalLength := len(data)
	if originalLength == 0 {
		return 0, nil
	}
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.truncated = true
		return originalLength, nil
	}
	if len(data) > remaining {
		data = data[:remaining]
		b.truncated = true
	}
	_, _ = b.buffer.Write(data)
	return originalLength, nil
}

func (b *limitedBuffer) String() string {
	if !b.truncated {
		return b.buffer.String()
	}
	return b.buffer.String() + "\n... output truncated by AegisCodeAgent ..."
}

func (b *limitedBuffer) Truncated() bool { return b.truncated }
