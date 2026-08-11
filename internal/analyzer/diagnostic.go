package analyzer

import (
	"bufio"
	"regexp"
	"strconv"
	"strings"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

var goDiagnosticPattern = regexp.MustCompile(`^\s*(.+\.go):(\d+)(?::(\d+))?:\s*(.+?)\s*$`)

type diagnostic struct {
	Path    string
	Line    int
	Column  int
	Message string
	Raw     string
}

func parseDiagnosticLine(repository, line string) (diagnostic, bool) {
	matches := goDiagnosticPattern.FindStringSubmatch(line)
	if matches == nil {
		return diagnostic{}, false
	}
	lineNumber, err := strconv.Atoi(matches[2])
	if err != nil {
		return diagnostic{}, false
	}
	column := 0
	if matches[3] != "" {
		column, _ = strconv.Atoi(matches[3])
	}
	return diagnostic{
		Path:    normalizePath(repository, strings.TrimSpace(matches[1])),
		Line:    lineNumber,
		Column:  column,
		Message: strings.TrimSpace(matches[4]),
		Raw:     strings.TrimSpace(line),
	}, true
}

func parsePosition(repository, position string) (review.Location, bool) {
	diagnostic, ok := parseDiagnosticLine(repository, position+": position")
	if !ok {
		return review.Location{}, false
	}
	return review.Location{Path: diagnostic.Path, StartLine: diagnostic.Line}, true
}

func scanLines(value string, visit func(string)) {
	scanner := bufio.NewScanner(strings.NewReader(value))
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		visit(scanner.Text())
	}
}

func shortText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= limit {
		return value
	}
	if limit <= 1 {
		return value[:limit]
	}
	return value[:limit-1] + "…"
}

func tailText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return "…" + value[len(value)-limit+1:]
}

func commandFailure(tool string, execution Execution) error {
	detail := tailText(execution.CombinedOutput(), 1200)
	if detail == "" {
		detail = "command returned a non-zero exit code"
	}
	return &AnalyzerError{Tool: tool, ExitCode: execution.ExitCode, Detail: detail}
}

type AnalyzerError struct {
	Tool     string
	ExitCode int
	Detail   string
}

func (e *AnalyzerError) Error() string {
	return e.Tool + " failed with exit code " + strconv.Itoa(e.ExitCode) + ": " + e.Detail
}
