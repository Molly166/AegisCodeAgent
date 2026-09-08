package review

import (
	"path/filepath"
	"strings"
)

// HasReviewableSourceChange is the shared reasoning scope contract. A skipped
// model run satisfies a required-agent policy only outside this scope.
func HasReviewableSourceChange(files []ChangedFile) bool {
	for _, file := range files {
		if file.Binary || file.Status == FileStatusDeleted || file.NewPath == "" {
			continue
		}
		hidden := false
		for _, part := range strings.Split(filepath.ToSlash(file.NewPath), "/") {
			if strings.HasPrefix(part, ".") && part != ".github" {
				hidden = true
				break
			}
		}
		if hidden {
			continue
		}
		switch strings.ToLower(filepath.Ext(file.NewPath)) {
		case ".go", ".mod", ".sum", ".json", ".yaml", ".yml", ".toml", ".sql", ".proto":
			return true
		}
	}
	return false
}
