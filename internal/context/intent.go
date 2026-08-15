package repocontext

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

const (
	defaultIntentMaxBytes = 24 * 1024
	maxGitHubEventBytes   = 2 * 1024 * 1024
)

var issueReferencePattern = regexp.MustCompile(`(?i)(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)?\s*#([0-9]+)`)

var guidancePaths = []string{
	"AGENTS.md",
	"CONTRIBUTING.md",
	"SECURITY.md",
	".github/copilot-instructions.md",
}

// BuildChangeIntent combines provider metadata with bounded repository
// guidance. Every value is treated as untrusted review evidence by the Agent.
func BuildChangeIntent(repository, githubEventPath string, maximumBytes int) (review.ChangeIntent, []string) {
	if maximumBytes <= 0 {
		maximumBytes = defaultIntentMaxBytes
	}
	intent := review.ChangeIntent{
		Labels: []string{}, LinkedIssues: []string{}, RepositoryGuidance: []review.IntentDocument{},
	}
	warnings := make([]string, 0)
	remaining := maximumBytes

	if strings.TrimSpace(githubEventPath) != "" {
		event, err := readPullRequestEvent(githubEventPath)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("change intent could not read GitHub event: %v", err))
		} else if event.PullRequest.Title != "" || event.PullRequest.Body != "" {
			intent.Source = "github_pull_request"
			intent.Title, remaining, intent.Truncated = takeIntentText(event.PullRequest.Title, remaining, intent.Truncated)
			intent.Description, remaining, intent.Truncated = takeIntentText(event.PullRequest.Body, remaining, intent.Truncated)
			for _, label := range event.PullRequest.Labels {
				name := truncateIntentText(strings.TrimSpace(label.Name), 100)
				if name != "" && len(intent.Labels) < 32 {
					intent.Labels = append(intent.Labels, name)
				}
			}
		}
	}
	intent.LinkedIssues = extractIssueReferences(intent.Title + "\n" + intent.Description)

	for _, relative := range guidancePaths {
		content, exists, err := readRepositoryGuidance(repository, relative, remaining)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("change intent skipped %s: %v", relative, err))
			continue
		}
		if !exists {
			continue
		}
		if intent.Source == "" {
			intent.Source = "repository_guidance"
		}
		if content == "" {
			intent.Truncated = true
			break
		}
		intent.RepositoryGuidance = append(intent.RepositoryGuidance, review.IntentDocument{Path: relative, Content: content})
		remaining -= len(content)
		if remaining <= 0 {
			intent.Truncated = true
			break
		}
	}
	sort.Strings(intent.Labels)
	return intent, uniqueIntentStrings(warnings)
}

type pullRequestEvent struct {
	PullRequest struct {
		Title  string `json:"title"`
		Body   string `json:"body"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	} `json:"pull_request"`
}

func readPullRequestEvent(path string) (pullRequestEvent, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return pullRequestEvent{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return pullRequestEvent{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxGitHubEventBytes {
		return pullRequestEvent{}, fmt.Errorf("event must be a regular file no larger than %d bytes", maxGitHubEventBytes)
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxGitHubEventBytes+1))
	var event pullRequestEvent
	if err := decoder.Decode(&event); err != nil {
		return pullRequestEvent{}, fmt.Errorf("decode event: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values are not allowed")
		}
		return pullRequestEvent{}, fmt.Errorf("decode event: %w", err)
	}
	return event, nil
}

func readRepositoryGuidance(repository, relative string, maximumBytes int) (string, bool, error) {
	path := filepath.Join(repository, filepath.FromSlash(relative))
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", true, errors.New("symbolic links are not accepted")
	}
	if !info.Mode().IsRegular() || info.Size() > defaultMaxFileBytes {
		return "", true, errors.New("guidance must be a bounded regular file")
	}
	if maximumBytes <= 0 {
		return "", true, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return "", true, err
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, int64(maximumBytes)+1))
	if err != nil {
		return "", true, err
	}
	return truncateIntentText(string(content), maximumBytes), true, nil
}

func takeIntentText(value string, remaining int, alreadyTruncated bool) (string, int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", remaining, alreadyTruncated
	}
	if remaining <= 0 {
		return "", 0, true
	}
	truncated := truncateIntentText(value, remaining)
	wasTruncated := len(truncated) < len(value)
	return truncated, remaining - len(truncated), alreadyTruncated || wasTruncated
}

func truncateIntentText(value string, maximum int) string {
	if maximum <= 0 {
		return ""
	}
	if len(value) <= maximum {
		return value
	}
	value = value[:maximum]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return strings.TrimSpace(value)
}

func extractIssueReferences(value string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, match := range issueReferencePattern.FindAllStringSubmatch(value, 64) {
		if len(match) < 2 || match[1] == "" {
			continue
		}
		reference := "#" + match[1]
		if _, ok := seen[reference]; ok {
			continue
		}
		seen[reference] = struct{}{}
		result = append(result, reference)
		if len(result) == 32 {
			break
		}
	}
	return result
}

func uniqueIntentStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
