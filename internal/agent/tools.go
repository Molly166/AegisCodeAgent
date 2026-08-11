package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

const (
	maxReadLines        = 160
	maxToolOutputBytes  = 32 * 1024
	maxSearchFileBytes  = 1024 * 1024
	maxSearchTotalBytes = 8 * 1024 * 1024
)

type RepositoryTools struct {
	repository string
}

func NewRepositoryTools(repository string) (RepositoryTools, error) {
	resolved, err := filepath.EvalSymlinks(repository)
	if err != nil {
		return RepositoryTools{}, fmt.Errorf("resolve repository: %w", err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return RepositoryTools{}, fmt.Errorf("make repository path absolute: %w", err)
	}
	return RepositoryTools{repository: resolved}, nil
}

func (t RepositoryTools) Definitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "read_file_lines",
			Description: "Read a bounded line range from one text file inside the reviewed repository.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":       map[string]any{"type": "string", "description": "Repository-relative file path."},
					"start_line": map[string]any{"type": "integer", "minimum": 1},
					"end_line":   map[string]any{"type": "integer", "minimum": 1},
				},
				"required":             []string{"path", "start_line", "end_line"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "search_code",
			Description: "Search source text in the repository with bounded results; query is treated as plain text, not a regular expression.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":          map[string]any{"type": "string", "description": "Plain-text search query."},
					"path":           map[string]any{"type": "string", "description": "Repository-relative file or directory, or an empty string for the whole repository."},
					"max_results":    map[string]any{"type": "integer", "minimum": 1, "maximum": 30},
					"case_sensitive": map[string]any{"type": "boolean"},
				},
				"required":             []string{"query", "path", "max_results", "case_sensitive"},
				"additionalProperties": false,
			},
		},
	}
}

func (t RepositoryTools) Execute(ctx context.Context, call ToolCall) ToolResult {
	started := time.Now()
	var content, summary string
	var err error
	switch call.Name {
	case "read_file_lines":
		content, summary, err = t.readFileLines(ctx, call.Arguments)
	case "search_code":
		content, summary, err = t.searchCode(ctx, call.Arguments)
	default:
		err = fmt.Errorf("tool %q is not available", call.Name)
	}
	result := ToolResult{Content: content, Status: review.AgentToolSucceeded, Summary: summary, Duration: time.Since(started)}
	if err != nil {
		result.Status = review.AgentToolRejected
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrUnexpectedEOF) {
			result.Status = review.AgentToolFailed
		}
		result.Content = marshalToolOutput(map[string]any{"ok": false, "error": err.Error()})
		result.Summary = truncateText(err.Error(), 240)
	}
	return result
}

func (t RepositoryTools) readFileLines(ctx context.Context, raw json.RawMessage) (string, string, error) {
	var arguments struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
	}
	if err := decodeToolArguments(raw, &arguments); err != nil {
		return "", "", err
	}
	if arguments.StartLine < 1 || arguments.EndLine < arguments.StartLine {
		return "", "", errors.New("start_line and end_line must form a positive range")
	}
	if arguments.EndLine-arguments.StartLine+1 > maxReadLines {
		return "", "", fmt.Errorf("requested range exceeds %d lines", maxReadLines)
	}
	path, relative, err := t.resolvePath(arguments.Path)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", "", fmt.Errorf("stat file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", "", errors.New("path must reference a regular file")
	}
	if !searchableFile(path) {
		return "", "", errors.New("path is not an allowed source or project text file")
	}
	if info.Size() > maxSearchFileBytes {
		return "", "", fmt.Errorf("file exceeds %d byte tool limit", maxSearchFileBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", "", fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	lines := make([]map[string]any, 0, arguments.EndLine-arguments.StartLine+1)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 16*1024), maxSearchFileBytes)
	lineNumber := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		lineNumber++
		if lineNumber < arguments.StartLine {
			continue
		}
		if lineNumber > arguments.EndLine {
			break
		}
		lines = append(lines, map[string]any{"line": lineNumber, "text": truncateText(scanner.Text(), 1200)})
	}
	if err := scanner.Err(); err != nil {
		return "", "", fmt.Errorf("read file: %w", err)
	}
	output := marshalToolOutput(map[string]any{
		"ok": true, "path": relative, "requested_start": arguments.StartLine,
		"requested_end": arguments.EndLine, "lines": lines,
	})
	return output, fmt.Sprintf("read %d line(s) from %s", len(lines), relative), nil
}

func (t RepositoryTools) searchCode(ctx context.Context, raw json.RawMessage) (string, string, error) {
	var arguments struct {
		Query         string `json:"query"`
		Path          string `json:"path"`
		MaxResults    int    `json:"max_results"`
		CaseSensitive bool   `json:"case_sensitive"`
	}
	if err := decodeToolArguments(raw, &arguments); err != nil {
		return "", "", err
	}
	arguments.Query = strings.TrimSpace(arguments.Query)
	if utf8.RuneCountInString(arguments.Query) < 2 || utf8.RuneCountInString(arguments.Query) > 128 {
		return "", "", errors.New("query length must be between 2 and 128 characters")
	}
	if arguments.MaxResults < 1 || arguments.MaxResults > 30 {
		return "", "", errors.New("max_results must be between 1 and 30")
	}
	searchRoot := t.repository
	if strings.TrimSpace(arguments.Path) != "" {
		resolved, _, err := t.resolvePath(arguments.Path)
		if err != nil {
			return "", "", err
		}
		searchRoot = resolved
	}
	query := arguments.Query
	if !arguments.CaseSensitive {
		query = strings.ToLower(query)
	}
	type match struct {
		Path string `json:"path"`
		Line int    `json:"line"`
		Text string `json:"text"`
	}
	matches := make([]match, 0, arguments.MaxResults)
	bytesScanned := int64(0)
	err := filepath.WalkDir(searchRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path != searchRoot && shouldSkipDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		relativePath, relativeErr := filepath.Rel(t.repository, path)
		if relativeErr != nil || !allowedToolPath(relativePath) {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !searchableFile(path) {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxSearchFileBytes {
			return nil
		}
		bytesScanned += info.Size()
		if bytesScanned > maxSearchTotalBytes {
			return io.ErrUnexpectedEOF
		}
		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 16*1024), maxSearchFileBytes)
		lineNumber := 0
		for scanner.Scan() {
			lineNumber++
			line := scanner.Text()
			haystack := line
			if !arguments.CaseSensitive {
				haystack = strings.ToLower(line)
			}
			if strings.Contains(haystack, query) {
				matches = append(matches, match{Path: filepath.ToSlash(relativePath), Line: lineNumber, Text: truncateText(strings.TrimSpace(line), 500)})
				if len(matches) >= arguments.MaxResults {
					break
				}
			}
		}
		scanErr := scanner.Err()
		closeErr := file.Close()
		if scanErr != nil {
			return fmt.Errorf("scan %s: %w", path, scanErr)
		}
		if closeErr != nil {
			return closeErr
		}
		if len(matches) >= arguments.MaxResults {
			return fs.SkipAll
		}
		return nil
	})
	truncated := errors.Is(err, io.ErrUnexpectedEOF) || len(matches) >= arguments.MaxResults
	if err != nil && !errors.Is(err, fs.SkipAll) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", "", fmt.Errorf("search repository: %w", err)
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Path != matches[j].Path {
			return matches[i].Path < matches[j].Path
		}
		return matches[i].Line < matches[j].Line
	})
	output := marshalToolOutput(map[string]any{"ok": true, "query": arguments.Query, "matches": matches, "truncated": truncated})
	return output, fmt.Sprintf("found %d match(es) for %q", len(matches), truncateText(arguments.Query, 80)), nil
}

func (t RepositoryTools) resolvePath(candidate string) (string, string, error) {
	if strings.TrimSpace(candidate) == "" || filepath.IsAbs(candidate) {
		return "", "", errors.New("path must be a non-empty repository-relative path")
	}
	clean := filepath.Clean(filepath.FromSlash(candidate))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", errors.New("path escapes the reviewed repository")
	}
	joined := filepath.Join(t.repository, clean)
	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", "", fmt.Errorf("resolve path: %w", err)
	}
	relative, err := filepath.Rel(t.repository, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", errors.New("resolved path escapes the reviewed repository")
	}
	if !allowedToolPath(relative) {
		return "", "", errors.New("hidden or sensitive repository paths are not available to agent tools")
	}
	return resolved, filepath.ToSlash(relative), nil
}

func decodeToolArguments(raw json.RawMessage, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid tool arguments: %w", err)
	}
	if err := ensureDecoderEOF(decoder); err != nil {
		return fmt.Errorf("invalid tool arguments: %w", err)
	}
	return nil
}

func ensureDecoderEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("multiple JSON values are not allowed")
}

func shouldSkipDirectory(name string) bool {
	return name == ".git" || name == "vendor" || name == "node_modules" || (strings.HasPrefix(name, ".") && name != ".github")
}

func allowedToolPath(path string) bool {
	for _, component := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.HasPrefix(component, ".") && component != ".github" {
			return false
		}
	}
	return true
}

func searchableFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".mod", ".sum", ".json", ".yaml", ".yml", ".toml", ".sql", ".proto", ".md":
		return true
	default:
		return false
	}
}

func marshalToolOutput(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `{"ok":false,"error":"tool output encoding failed"}`
	}
	if len(encoded) <= maxToolOutputBytes {
		return string(encoded)
	}
	previewBytes := maxToolOutputBytes - 256
	for previewBytes > 256 {
		preview := truncateText(string(encoded), previewBytes)
		bounded, marshalErr := json.Marshal(map[string]any{
			"ok": true, "truncated": true, "preview": preview,
		})
		if marshalErr != nil {
			return `{"ok":false,"error":"tool output encoding failed"}`
		}
		if len(bounded) <= maxToolOutputBytes {
			return string(bounded)
		}
		previewBytes /= 2
	}
	return `{"ok":false,"error":"tool output exceeded its byte limit"}`
}

func truncateText(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + "…"
}
