package gitdiff

import (
	"strings"
	"testing"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

func TestParseModifiedFileTracksLineNumbers(t *testing.T) {
	diff := `diff --git a/internal/calc.go b/internal/calc.go
index 1111111..2222222 100644
--- a/internal/calc.go
+++ b/internal/calc.go
@@ -10,3 +10,4 @@ func Sum(values []int) int {
     total := 0
-	return total
+	total += values[0]
+	return total
 }
`
	files, err := Parse(strings.NewReader(diff))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("file count = %d, want 1", len(files))
	}
	file := files[0]
	if file.Status != review.FileStatusModified || file.Path() != "internal/calc.go" {
		t.Fatalf("unexpected file: %+v", file)
	}
	if file.Stats.Additions != 2 || file.Stats.Deletions != 1 {
		t.Fatalf("unexpected stats: %+v", file.Stats)
	}
	if len(file.Hunks) != 1 || len(file.Hunks[0].Lines) != 5 {
		t.Fatalf("unexpected hunks: %+v", file.Hunks)
	}
	deletion := file.Hunks[0].Lines[1]
	addition := file.Hunks[0].Lines[2]
	if deletion.Kind != review.LineDeletion || deletion.OldLine != 11 || deletion.NewLine != 0 {
		t.Fatalf("unexpected deletion coordinates: %+v", deletion)
	}
	if addition.Kind != review.LineAddition || addition.OldLine != 0 || addition.NewLine != 11 {
		t.Fatalf("unexpected addition coordinates: %+v", addition)
	}
}

func TestParseFileKinds(t *testing.T) {
	diff := `diff --git a/config.json b/config.json
new file mode 100644
index 0000000..1111111
--- /dev/null
+++ b/config.json
@@ -0,0 +1,2 @@
+{}
+
diff --git a/legacy.go b/legacy.go
deleted file mode 100644
index 1111111..0000000
--- a/legacy.go
+++ /dev/null
@@ -1 +0,0 @@
-package legacy
diff --git "a/old name.txt" "b/new name.txt"
similarity index 100%
rename from old name.txt
rename to new name.txt
diff --git a/logo.png b/logo.png
new file mode 100644
index 0000000..2222222
Binary files /dev/null and b/logo.png differ
`
	files, err := Parse(strings.NewReader(diff))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(files) != 4 {
		t.Fatalf("file count = %d, want 4", len(files))
	}
	if files[0].Status != review.FileStatusAdded || files[0].OldPath != "" || files[0].Stats.Additions != 2 {
		t.Errorf("unexpected added file: %+v", files[0])
	}
	if files[1].Status != review.FileStatusDeleted || files[1].NewPath != "" || files[1].Stats.Deletions != 1 {
		t.Errorf("unexpected deleted file: %+v", files[1])
	}
	if files[2].Status != review.FileStatusRenamed || files[2].OldPath != "old name.txt" || files[2].NewPath != "new name.txt" {
		t.Errorf("unexpected renamed file: %+v", files[2])
	}
	if files[3].Status != review.FileStatusAdded || !files[3].Binary {
		t.Errorf("unexpected binary file: %+v", files[3])
	}
}

func TestParseGitQuotedUnicodePath(t *testing.T) {
	oldPath, newPath, err := parseDiffHeader(`diff --git "a/docs/\346\265\213\350\257\225.go" "b/docs/\346\265\213\350\257\225.go"`)
	if err != nil {
		t.Fatalf("parseDiffHeader() error = %v", err)
	}
	if oldPath != "docs/测试.go" || newPath != "docs/测试.go" {
		t.Fatalf("paths = %q, %q", oldPath, newPath)
	}
}

func TestParseRejectsMalformedHunk(t *testing.T) {
	diff := "diff --git a/a.go b/a.go\n@@ invalid @@\n"
	if _, err := Parse(strings.NewReader(diff)); err == nil {
		t.Fatal("Parse() error = nil, want malformed hunk error")
	}
}
