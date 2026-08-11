package analyzer

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

const (
	NameGoTest      = "go-test"
	NameGoVet       = "go-vet"
	NameStaticcheck = "staticcheck"
	NameGosec       = "gosec"
)

var defaultNames = []string{NameGoTest, NameGoVet}
var allNames = []string{NameGoTest, NameGoVet, NameStaticcheck, NameGosec}

type Input struct {
	Repository   string
	Packages     []string
	ChangedLines ChangedLineSet
}

type Analyzer interface {
	Name() string
	Analyze(ctx context.Context, input Input) ([]review.Finding, error)
}

type Command struct {
	Name      string
	Arguments []string
	Directory string
}

type Execution struct {
	Stdout    string
	Stderr    string
	ExitCode  int
	Duration  time.Duration
	Truncated bool
}

func (e Execution) CombinedOutput() string {
	switch {
	case e.Stdout == "":
		return e.Stderr
	case e.Stderr == "":
		return e.Stdout
	default:
		return e.Stdout + "\n" + e.Stderr
	}
}

type Runner interface {
	LookPath(name string) (string, error)
	Run(ctx context.Context, command Command) (Execution, error)
}

type SkipKind string

const (
	SkipNotApplicable SkipKind = "not_applicable"
	SkipUnavailable   SkipKind = "unavailable"
)

type SkipError struct {
	Kind   SkipKind
	Reason string
}

func (e *SkipError) Error() string { return e.Reason }

func skip(kind SkipKind, reason string) error {
	return &SkipError{Kind: kind, Reason: reason}
}

func ensureTool(runner Runner, name string) error {
	if _, err := runner.LookPath(name); err != nil {
		return skip(SkipUnavailable, fmt.Sprintf("%s is not installed or not available on PATH", name))
	}
	return nil
}

func requirePackages(input Input) error {
	if len(input.Packages) == 0 {
		return skip(SkipNotApplicable, "no affected Go packages")
	}
	return nil
}

func ParseSelection(value string) ([]string, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "", "default":
		return append([]string(nil), defaultNames...), nil
	case "all":
		return append([]string(nil), allNames...), nil
	case "none":
		return []string{}, nil
	}

	allowed := make(map[string]struct{}, len(allNames))
	for _, name := range allNames {
		allowed[name] = struct{}{}
	}
	seen := make(map[string]struct{})
	selection := make([]string, 0)
	for _, rawName := range strings.Split(value, ",") {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, errors.New("analyzer selection contains an empty name")
		}
		if _, ok := allowed[name]; !ok {
			return nil, fmt.Errorf("unknown analyzer %q (supported: %s)", name, strings.Join(allNames, ", "))
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		selection = append(selection, name)
	}
	return selection, nil
}

func AvailableNames() []string {
	names := append([]string(nil), allNames...)
	sort.Strings(names)
	return names
}
