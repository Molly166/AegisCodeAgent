package analyzer

import (
	"context"
	"errors"
	"sync"
)

type fakeRunner struct {
	mu         sync.Mutex
	available  map[string]bool
	executions map[string]Execution
	errors     map[string]error
	commands   []Command
}

func (r *fakeRunner) LookPath(name string) (string, error) {
	if r.available[name] {
		return "/tools/" + name, nil
	}
	return "", errors.New("tool not found")
}

func (r *fakeRunner) Run(_ context.Context, command Command) (Execution, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, command)
	return r.executions[command.Name], r.errors[command.Name]
}
