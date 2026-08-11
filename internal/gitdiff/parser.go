package gitdiff

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

var hunkHeaderPattern = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)$`)

func Parse(r io.Reader) ([]review.ChangedFile, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)

	files := make([]review.ChangedFile, 0)
	var currentFile *review.ChangedFile
	var currentHunk *review.Hunk
	oldLine, newLine := 0, 0
	lineNumber := 0

	finishHunk := func() {
		if currentFile != nil && currentHunk != nil {
			currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
			currentHunk = nil
		}
	}
	finishFile := func() {
		if currentFile == nil {
			return
		}
		finishHunk()
		inferStatus(currentFile)
		if currentFile.Hunks == nil {
			currentFile.Hunks = []review.Hunk{}
		}
		files = append(files, *currentFile)
		currentFile = nil
	}

	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()

		if strings.HasPrefix(line, "diff --git ") {
			finishFile()
			oldPath, newPath, err := parseDiffHeader(line)
			if err != nil {
				return nil, fmt.Errorf("parse diff header at line %d: %w", lineNumber, err)
			}
			currentFile = &review.ChangedFile{
				OldPath: oldPath,
				NewPath: newPath,
				Status:  review.FileStatusModified,
				Hunks:   make([]review.Hunk, 0),
			}
			continue
		}

		if currentFile == nil {
			continue
		}

		if strings.HasPrefix(line, "@@ ") {
			finishHunk()
			hunk, err := parseHunkHeader(line)
			if err != nil {
				return nil, fmt.Errorf("parse hunk header at line %d: %w", lineNumber, err)
			}
			currentHunk = &hunk
			oldLine, newLine = hunk.OldStart, hunk.NewStart
			continue
		}

		if currentHunk != nil {
			if line == `\ No newline at end of file` {
				continue
			}
			if line == "" {
				return nil, fmt.Errorf("invalid empty diff line inside hunk at line %d", lineNumber)
			}
			switch line[0] {
			case ' ':
				currentHunk.Lines = append(currentHunk.Lines, review.DiffLine{
					Kind: review.LineContext, Content: line[1:], OldLine: oldLine, NewLine: newLine,
				})
				oldLine++
				newLine++
			case '+':
				currentHunk.Lines = append(currentHunk.Lines, review.DiffLine{
					Kind: review.LineAddition, Content: line[1:], NewLine: newLine,
				})
				currentFile.Stats.Additions++
				newLine++
			case '-':
				currentHunk.Lines = append(currentHunk.Lines, review.DiffLine{
					Kind: review.LineDeletion, Content: line[1:], OldLine: oldLine,
				})
				currentFile.Stats.Deletions++
				oldLine++
			default:
				return nil, fmt.Errorf("invalid diff marker %q inside hunk at line %d", line[0], lineNumber)
			}
			continue
		}

		switch {
		case strings.HasPrefix(line, "new file mode "):
			currentFile.Status = review.FileStatusAdded
			currentFile.OldPath = ""
		case strings.HasPrefix(line, "deleted file mode "):
			currentFile.Status = review.FileStatusDeleted
			currentFile.NewPath = ""
		case strings.HasPrefix(line, "rename from "):
			path, err := parsePathLiteral(strings.TrimPrefix(line, "rename from "))
			if err != nil {
				return nil, fmt.Errorf("parse rename source at line %d: %w", lineNumber, err)
			}
			currentFile.OldPath = path
			currentFile.Status = review.FileStatusRenamed
		case strings.HasPrefix(line, "rename to "):
			path, err := parsePathLiteral(strings.TrimPrefix(line, "rename to "))
			if err != nil {
				return nil, fmt.Errorf("parse rename destination at line %d: %w", lineNumber, err)
			}
			currentFile.NewPath = path
			currentFile.Status = review.FileStatusRenamed
		case strings.HasPrefix(line, "Binary files "), line == "GIT binary patch":
			currentFile.Binary = true
		case strings.HasPrefix(line, "--- "):
			path, err := parseFileMarkerPath(strings.TrimPrefix(line, "--- "))
			if err != nil {
				return nil, fmt.Errorf("parse old path at line %d: %w", lineNumber, err)
			}
			currentFile.OldPath = path
		case strings.HasPrefix(line, "+++ "):
			path, err := parseFileMarkerPath(strings.TrimPrefix(line, "+++ "))
			if err != nil {
				return nil, fmt.Errorf("parse new path at line %d: %w", lineNumber, err)
			}
			currentFile.NewPath = path
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read diff: %w", err)
	}
	finishFile()
	return files, nil
}

func parseHunkHeader(line string) (review.Hunk, error) {
	matches := hunkHeaderPattern.FindStringSubmatch(line)
	if matches == nil {
		return review.Hunk{}, fmt.Errorf("unsupported header %q", line)
	}
	oldStart, err := parseNumber(matches[1], "old start")
	if err != nil {
		return review.Hunk{}, err
	}
	oldCount, err := parseCount(matches[2], "old count")
	if err != nil {
		return review.Hunk{}, err
	}
	newStart, err := parseNumber(matches[3], "new start")
	if err != nil {
		return review.Hunk{}, err
	}
	newCount, err := parseCount(matches[4], "new count")
	if err != nil {
		return review.Hunk{}, err
	}
	return review.Hunk{
		OldStart: oldStart,
		OldLines: oldCount,
		NewStart: newStart,
		NewLines: newCount,
		Section:  strings.TrimSpace(matches[5]),
		Lines:    make([]review.DiffLine, 0),
	}, nil
}

func parseCount(value, label string) (int, error) {
	if value == "" {
		return 1, nil
	}
	return parseNumber(value, label)
}

func parseNumber(value, label string) (int, error) {
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", label, value, err)
	}
	return number, nil
}

func parseDiffHeader(line string) (string, string, error) {
	rest := strings.TrimPrefix(line, "diff --git ")
	oldToken, rest, err := consumeGitToken(rest)
	if err != nil {
		return "", "", err
	}
	newToken, trailing, err := consumeGitToken(rest)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(trailing) != "" {
		return "", "", fmt.Errorf("unexpected trailing content %q", trailing)
	}
	return stripGitPrefix(oldToken), stripGitPrefix(newToken), nil
}

func consumeGitToken(input string) (string, string, error) {
	input = strings.TrimLeft(input, " ")
	if input == "" {
		return "", "", fmt.Errorf("missing path")
	}
	if input[0] != '"' {
		if index := strings.IndexByte(input, ' '); index >= 0 {
			return input[:index], input[index+1:], nil
		}
		return input, "", nil
	}
	escaped := false
	for index := 1; index < len(input); index++ {
		switch {
		case escaped:
			escaped = false
		case input[index] == '\\':
			escaped = true
		case input[index] == '"':
			value, err := strconv.Unquote(input[:index+1])
			if err != nil {
				return "", "", fmt.Errorf("unquote path: %w", err)
			}
			return value, input[index+1:], nil
		}
	}
	return "", "", fmt.Errorf("unterminated quoted path")
}

func parseFileMarkerPath(value string) (string, error) {
	if value == "/dev/null" {
		return "", nil
	}
	if strings.HasPrefix(value, "\"") {
		path, rest, err := consumeGitToken(value)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(rest) != "" {
			return "", fmt.Errorf("unexpected trailing content %q", rest)
		}
		return stripGitPrefix(path), nil
	}
	if index := strings.IndexByte(value, '\t'); index >= 0 {
		value = value[:index]
	}
	return stripGitPrefix(value), nil
}

func parsePathLiteral(value string) (string, error) {
	if !strings.HasPrefix(value, "\"") {
		return value, nil
	}
	path, rest, err := consumeGitToken(value)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(rest) != "" {
		return "", fmt.Errorf("unexpected trailing content %q", rest)
	}
	return path, nil
}

func stripGitPrefix(path string) string {
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		return path[2:]
	}
	return path
}

func inferStatus(file *review.ChangedFile) {
	if file.Status != review.FileStatusModified {
		return
	}
	switch {
	case file.OldPath == "" && file.NewPath != "":
		file.Status = review.FileStatusAdded
	case file.NewPath == "" && file.OldPath != "":
		file.Status = review.FileStatusDeleted
	case file.OldPath != file.NewPath:
		file.Status = review.FileStatusRenamed
	}
}
