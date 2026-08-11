package analyzer

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

const (
	ScopeChanged = "changed"
	ScopeAll     = "all"
)

func ValidateScope(scope string) error {
	if scope != ScopeChanged && scope != ScopeAll {
		return fmt.Errorf("unsupported analysis scope %q (supported: changed, all)", scope)
	}
	return nil
}

type ChangedLineSet struct {
	repository string
	lines      map[string]map[int]struct{}
}

func SelectPackages(repository string, files []review.ChangedFile, scope string) ([]string, error) {
	if err := ValidateScope(scope); err != nil {
		return nil, err
	}
	switch scope {
	case ScopeAll:
		return []string{"./..."}, nil
	case ScopeChanged:
	}

	packages := make(map[string]struct{})
	for _, file := range files {
		paths := []string{file.NewPath}
		if file.Status == review.FileStatusDeleted || file.Status == review.FileStatusRenamed {
			paths = append(paths, file.OldPath)
		}
		for _, path := range paths {
			path = filepath.ToSlash(filepath.Clean(path))
			if path == "." || path == "" || !isSafeRelativePath(path) {
				continue
			}
			base := filepath.Base(path)
			if path == "go.mod" || path == "go.sum" || path == "go.work" || path == "go.work.sum" {
				return []string{"./..."}, nil
			}
			if filepath.Ext(base) != ".go" {
				continue
			}
			directory := filepath.Dir(path)
			absoluteDirectory := filepath.Join(repository, filepath.FromSlash(directory))
			info, err := os.Stat(absoluteDirectory)
			if err != nil || !info.IsDir() {
				continue
			}
			if directory == "." {
				packages["."] = struct{}{}
			} else {
				packages["./"+filepath.ToSlash(directory)] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(packages))
	for packagePattern := range packages {
		result = append(result, packagePattern)
	}
	sort.Strings(result)
	return result, nil
}

func BuildChangedLineSet(repository string, files []review.ChangedFile) ChangedLineSet {
	set := ChangedLineSet{repository: repository, lines: make(map[string]map[int]struct{})}
	for _, file := range files {
		if file.NewPath == "" {
			continue
		}
		path := normalizePath(repository, file.NewPath)
		if path == "" {
			continue
		}
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				if line.Kind != review.LineAddition || line.NewLine <= 0 {
					continue
				}
				if set.lines[path] == nil {
					set.lines[path] = make(map[int]struct{})
				}
				set.lines[path][line.NewLine] = struct{}{}
			}
		}
	}
	return set
}

func (s ChangedLineSet) Contains(location review.Location) bool {
	if location.Path == "" || location.StartLine <= 0 {
		return true
	}
	path := normalizePath(s.repository, location.Path)
	lines, ok := s.lines[path]
	if !ok {
		return false
	}
	end := location.EndLine
	if end < location.StartLine {
		end = location.StartLine
	}
	for line := location.StartLine; line <= end; line++ {
		if _, ok := lines[line]; ok {
			return true
		}
	}
	return false
}

func normalizePath(repository, path string) string {
	if path == "" {
		return ""
	}
	cleanPath := filepath.Clean(path)
	if filepath.IsAbs(cleanPath) {
		relative, err := filepath.Rel(repository, cleanPath)
		if err != nil || !isSafeRelativePath(relative) {
			return filepath.ToSlash(cleanPath)
		}
		cleanPath = relative
	}
	cleanPath = strings.TrimPrefix(filepath.ToSlash(cleanPath), "./")
	return cleanPath
}

func isSafeRelativePath(path string) bool {
	if filepath.IsAbs(path) {
		return false
	}
	cleanPath := filepath.Clean(path)
	return cleanPath != ".." && !strings.HasPrefix(cleanPath, ".."+string(filepath.Separator))
}
