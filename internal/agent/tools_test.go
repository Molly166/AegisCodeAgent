package agent

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

func TestRepositoryToolsReadSearchAndRejectTraversal(t *testing.T) {
	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "main.go"), []byte("package main\n\nfunc main() {\n\tprintln(\"needle\")\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, ".env"), []byte("DEEPSEEK_API_KEY=must-not-leak\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, ".secrets.json"), []byte(`{"token":"hidden-needle"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tools, err := NewRepositoryTools(repository)
	if err != nil {
		t.Fatal(err)
	}
	read := tools.Execute(context.Background(), ToolCall{
		Name: "read_file_lines", Arguments: json.RawMessage(`{"path":"main.go","start_line":3,"end_line":4}`),
	})
	if read.Status != review.AgentToolSucceeded || !strings.Contains(read.Content, "needle") {
		t.Fatalf("unexpected read result: %+v", read)
	}
	search := tools.Execute(context.Background(), ToolCall{
		Name: "search_code", Arguments: json.RawMessage(`{"query":"needle","path":"","max_results":5,"case_sensitive":true}`),
	})
	if search.Status != review.AgentToolSucceeded || !strings.Contains(search.Content, `"line":4`) {
		t.Fatalf("unexpected search result: %+v", search)
	}
	rejected := tools.Execute(context.Background(), ToolCall{
		Name: "read_file_lines", Arguments: json.RawMessage(`{"path":"../secret","start_line":1,"end_line":1}`),
	})
	if rejected.Status != review.AgentToolRejected || !strings.Contains(rejected.Content, "escapes") {
		t.Fatalf("traversal was not rejected: %+v", rejected)
	}
	secret := tools.Execute(context.Background(), ToolCall{
		Name: "read_file_lines", Arguments: json.RawMessage(`{"path":".env","start_line":1,"end_line":1}`),
	})
	if secret.Status != review.AgentToolRejected || strings.Contains(secret.Content, "must-not-leak") {
		t.Fatalf("secret path was not rejected: %+v", secret)
	}
	hiddenSearch := tools.Execute(context.Background(), ToolCall{
		Name: "search_code", Arguments: json.RawMessage(`{"query":"hidden-needle","path":"","max_results":5,"case_sensitive":true}`),
	})
	if hiddenSearch.Status != review.AgentToolSucceeded || strings.Contains(hiddenSearch.Content, ".secrets.json") {
		t.Fatalf("hidden file leaked through search: %+v", hiddenSearch)
	}
}

func TestRepositoryToolsRejectSymlinkEscape(t *testing.T) {
	repository := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.go")
	if err := os.WriteFile(outside, []byte("package secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repository, "link.go")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	tools, err := NewRepositoryTools(repository)
	if err != nil {
		t.Fatal(err)
	}
	result := tools.Execute(context.Background(), ToolCall{
		Name: "read_file_lines", Arguments: json.RawMessage(`{"path":"link.go","start_line":1,"end_line":1}`),
	})
	if result.Status != review.AgentToolRejected || !strings.Contains(result.Content, "escapes") {
		t.Fatalf("symlink escape was not rejected: %+v", result)
	}
	search := tools.Execute(context.Background(), ToolCall{
		Name: "search_code", Arguments: json.RawMessage(`{"query":"secret","path":"","max_results":5,"case_sensitive":true}`),
	})
	if search.Status != review.AgentToolSucceeded || strings.Contains(search.Content, "package secret") {
		t.Fatalf("symlink source leaked through search: %+v", search)
	}
}

func TestRepositoryToolsOpenSourceFileRejectsPathReplacement(t *testing.T) {
	for _, replaceDirectory := range []bool{false, true} {
		name := "file"
		if replaceDirectory {
			name = "parent-directory"
		}
		t.Run(name, func(t *testing.T) {
			repository := t.TempDir()
			sourceDir := filepath.Join(repository, "src")
			if err := os.Mkdir(sourceDir, 0o700); err != nil {
				t.Fatal(err)
			}
			sourceFile := filepath.Join(sourceDir, "main.go")
			if err := os.WriteFile(sourceFile, []byte("package safe\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			outside := t.TempDir()
			outsideFile := filepath.Join(outside, "main.go")
			if err := os.WriteFile(outsideFile, []byte("package private\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			tools, err := NewRepositoryTools(repository)
			if err != nil {
				t.Fatal(err)
			}
			_, checkedRelative, err := tools.resolvePath("src/main.go")
			if err != nil {
				t.Fatal(err)
			}
			// Deterministically replace the already validated path, modeling
			// a repository mutation between inspection and the actual open.
			replaced, target := sourceFile, outsideFile
			if replaceDirectory {
				replaced, target = sourceDir, outside
			}
			if err := os.Rename(replaced, replaced+".checked"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, replaced); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			file, _, err := tools.openSourceFile(checkedRelative)
			if file != nil {
				_ = file.Close()
			}
			if err == nil {
				t.Fatal("checked source path escaped repository after replacement")
			}
		})
	}
}

func TestRepositoryToolsOpenSourceFileKeepsRegularFileDescriptor(t *testing.T) {
	repository := t.TempDir()
	const content = "package safe\n"
	if err := os.WriteFile(filepath.Join(repository, "main.go"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	tools, err := NewRepositoryTools(repository)
	if err != nil {
		t.Fatal(err)
	}
	file, info, err := tools.openSourceFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil || string(data) != content || info.Size() != int64(len(content)) {
		t.Fatalf("root-bound regular file read failed: data=%q info=%v error=%v", data, info, err)
	}
	for _, candidate := range []string{"../main.go", filepath.Join(repository, "main.go"), ".secrets.json", "main.exe"} {
		opened, _, err := tools.openSourceFile(candidate)
		if opened != nil {
			_ = opened.Close()
		}
		if err == nil {
			t.Errorf("unsafe candidate %q was accepted", candidate)
		}
	}
}

func TestRepositoryToolsRejectUnknownAndMalformedCalls(t *testing.T) {
	tools, err := NewRepositoryTools(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []ToolCall{
		{Name: "write_file", Arguments: json.RawMessage(`{}`)},
		{Name: "read_file_lines", Arguments: json.RawMessage(`{"path":"x","start_line":1,"end_line":1,"extra":true}`)},
		{Name: "search_code", Arguments: json.RawMessage(`{"query":"x","path":"","max_results":100,"case_sensitive":true}`)},
	} {
		result := tools.Execute(context.Background(), call)
		if result.Status != review.AgentToolRejected {
			t.Fatalf("call=%+v result=%+v", call, result)
		}
	}
}
