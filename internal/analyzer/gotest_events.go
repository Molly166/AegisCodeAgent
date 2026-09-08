package analyzer

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

// Go 1.26 does not distinguish t.Log from t.Error in Output. Output is evidence,
// not a failure verdict: only terminal fail/build-fail events establish one.
type goTestEvent struct {
	Action      string `json:"Action"`
	Package     string `json:"Package"`
	Test        string `json:"Test"`
	ImportPath  string `json:"ImportPath"`
	FailedBuild string `json:"FailedBuild"`
	Output      string `json:"Output"`
}

type goTestKey struct{ pkg, test string }

type goTestState struct {
	key         goTestKey
	terminal    string
	failedBuild string
	output      strings.Builder
}

type goTestStream struct {
	tests      map[goTestKey]*goTestState
	order      []*goTestState
	builds     map[string]*goTestState
	buildOrder []*goTestState
	invalid    bool
}

func (s *goTestStream) test(key goTestKey) *goTestState {
	if state := s.tests[key]; state != nil {
		return state
	}
	state := &goTestState{key: key}
	s.tests[key] = state
	s.order = append(s.order, state)
	return state
}

func (s *goTestStream) build(importPath string) *goTestState {
	if state := s.builds[importPath]; state != nil {
		return state
	}
	state := &goTestState{key: goTestKey{pkg: importPath}}
	s.builds[importPath] = state
	s.buildOrder = append(s.buildOrder, state)
	return state
}

func (s *goTestStream) consume(text string, strict bool) {
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "{") {
			// Early Go/toolchain errors may be plain stderr. The exit status
			// handles these conservatively; never mine arbitrary text for P1s.
			if strict && line != "" {
				s.invalid = true
			}
			continue
		}
		var event goTestEvent
		if json.Unmarshal([]byte(line), &event) != nil || event.Action == "" {
			s.invalid = true
			continue
		}
		switch event.Action {
		case "build-output", "build-fail":
			if event.ImportPath == "" {
				s.invalid = true
				continue
			}
			state := s.build(event.ImportPath)
			state.output.WriteString(event.Output)
			if event.Action == "build-fail" {
				state.terminal = "fail"
			}
		case "start", "run", "output", "pass", "skip", "bench", "fail":
			if event.Package == "" {
				s.invalid = true
				continue
			}
			state := s.test(goTestKey{event.Package, event.Test})
			if event.Action == "output" {
				state.output.WriteString(event.Output)
			}
			switch event.Action {
			case "pass", "skip", "bench", "fail":
				if state.terminal != "" && state.terminal != event.Action {
					s.invalid = true
					// Preserve confirmed failure even when later data contradicts it.
					if state.terminal == "fail" {
						continue
					}
				}
				state.terminal = event.Action
				if event.Action == "fail" && event.FailedBuild != "" {
					state.failedBuild = event.FailedBuild
					s.build(event.FailedBuild).terminal = "fail"
				}
			}
		}
	}
	if scanner.Err() != nil {
		s.invalid = true
	}
}

func parseGoTestDiagnostics(repository string, execution Execution) ([]review.Finding, error) {
	stream := goTestStream{tests: make(map[goTestKey]*goTestState), builds: make(map[string]*goTestState)}
	stream.consume(execution.Stdout, true)
	stream.consume(execution.Stderr, false)
	if len(stream.order) == 0 && execution.ExitCode == 0 {
		stream.invalid = true // an empty stream cannot prove tests completed
	}
	module := goTestModule(repository)
	findings := make([]review.Finding, 0)
	for _, state := range stream.buildOrder {
		if state.terminal != "fail" {
			continue
		}
		// Compiler output is relative to the go command's working directory;
		// unlike test2json test output it must not get a package prefix.
		findings = append(findings, goTestFinding(repository, "", "Go package build failed", state, execution.Stderr))
	}
	for _, state := range stream.order {
		if state.terminal == "" {
			// An output/run prefix is not sufficient to claim a clean review.
			stream.invalid = true
		}
		if state.terminal != "fail" || state.failedBuild != "" {
			continue
		}
		if state.key.test == "" {
			if stream.hasFailedTest(state.key.pkg, "") {
				continue // package fail is the aggregate of failed tests
			}
			findings = append(findings, goTestFinding(repository, module, "Go package tests failed", state, ""))
			continue
		}
		if stream.hasFailedTest(state.key.pkg, state.key.test+"/") && !hasGoTestDiagnostic(repository, state.output.String()) {
			continue // omit a parent failure that only wraps failed subtests
		}
		findings = append(findings, goTestFinding(repository, module, "Go test failed", state, ""))
	}
	if stream.invalid {
		return findings, errors.New("go test JSON evidence is malformed or missing terminal test/package events")
	}
	return findings, nil
}

func (s *goTestStream) hasFailedTest(pkg, prefix string) bool {
	for _, state := range s.order {
		if state.key.pkg == pkg && state.key.test != "" && state.terminal == "fail" && strings.HasPrefix(state.key.test, prefix) {
			return true
		}
	}
	return false
}

func hasGoTestDiagnostic(repository, output string) bool {
	found := false
	scanLines(output, func(line string) {
		if _, ok := parseDiagnosticLine(repository, line); ok {
			found = true
		}
	})
	return found
}

func goTestFinding(repository, module, title string, state *goTestState, fallback string) review.Finding {
	output := state.output.String()
	location := goTestLocation(repository, module, state.key.pkg, output)
	if output == "" {
		// Global stderr is context only, not source evidence for this build.
		output = fallback
	}
	identity := state.key.pkg
	if state.key.test != "" {
		identity += " / " + state.key.test
	}
	return review.Finding{
		RuleID: NameGoTest, Title: title + ": " + identity,
		Description: "The go test JSON stream explicitly reported failure for " + identity + ". Log lines below are supporting context, not separate failure verdicts.",
		Severity:    review.SeverityHigh, Category: review.CategoryTesting,
		Location:   location,
		Evidence:   fmt.Sprintf("Action: fail\nPackage: %s\nTest: %s\n%s", state.key.pkg, state.key.test, tailText(output, 4000)),
		Suggestion: "Reproduce the failing test or package build with go test before merging.",
		Confidence: 1, Source: NameGoTest,
	}
}

// A failed test can contain ordinary t.Log calls. Only attach a location when
// its output names one unique source position; never guess which of several
// logged lines was the assertion. The terminal event, not that line, is proof.
func goTestLocation(repository, module, pkg, output string) review.Location {
	positions := make(map[review.Location]struct{})
	scanLines(output, func(line string) {
		matches := goDiagnosticPattern.FindStringSubmatch(line)
		diagnostic, ok := parseDiagnosticLine(repository, line)
		if !ok {
			return
		}
		location := review.Location{Path: diagnostic.Path, StartLine: diagnostic.Line}
		// Test2json prints filenames relative to the tested package, not repo.
		if !filepath.IsAbs(strings.TrimSpace(matches[1])) && isSafeRelativePath(location.Path) && module != "" {
			importPath, _, _ := strings.Cut(pkg, " [")
			if relative, ok := strings.CutPrefix(importPath, module+"/"); ok && isSafeRelativePath(relative) {
				location.Path = filepath.ToSlash(filepath.Join(relative, location.Path))
			} else if importPath != module {
				return // a dependency's basename is not a local source location
			}
		}
		positions[location] = struct{}{}
	})
	if len(positions) == 1 {
		for location := range positions {
			if goTestSourceContains(repository, location) {
				return location
			}
		}
	}
	return review.Location{}
}

func goTestSourceContains(repository string, location review.Location) bool {
	if !isSafeRelativePath(location.Path) || location.StartLine <= 0 {
		return false
	}
	data, err := readGoTestFile(repository, location.Path, 2*1024*1024)
	if err != nil {
		return false
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for line := 1; scanner.Scan(); line++ {
		if line == location.StartLine {
			return true
		}
	}
	return false
}

// Restrict optional source enrichment to bounded, regular files beneath the
// checkout. Untrusted diagnostic paths must not read outside repository root.
func readGoTestFile(repository, name string, limit int64) ([]byte, error) {
	root, err := os.OpenRoot(repository)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Stat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("go test source enrichment requires a bounded regular file")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("go test source enrichment requires a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("go test source enrichment exceeds size limit")
	}
	return data, nil
}

func goTestModule(repository string) string {
	data, err := readGoTestFile(repository, "go.mod", 64*1024)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "module" {
			continue
		}
		value := fields[1]
		if strings.HasPrefix(value, "\"") || strings.HasPrefix(value, "`") {
			value, _ = strconv.Unquote(value)
		}
		return value
	}
	return ""
}
