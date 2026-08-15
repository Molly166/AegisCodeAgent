package repocontext

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildChangeIntentCombinesPullRequestAndRepositoryGuidance(t *testing.T) {
	repository := t.TempDir()
	writeContextFile(t, filepath.Join(repository, "AGENTS.md"), "All security boundaries require tests.\n")
	eventPath := filepath.Join(t.TempDir(), "event.json")
	writeContextFile(t, eventPath, `{
  "pull_request": {
    "title": "Harden child-process isolation",
    "body": "Prevent credential propagation. Fixes #42 and relates to #7.",
    "labels": [{"name": "security"}, {"name": "agent"}]
  }
}`)

	intent, warnings := BuildChangeIntent(repository, eventPath, 16*1024)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if intent.Source != "github_pull_request" || intent.Title != "Harden child-process isolation" {
		t.Fatalf("pull-request intent was not loaded: %+v", intent)
	}
	if strings.Join(intent.Labels, ",") != "agent,security" || strings.Join(intent.LinkedIssues, ",") != "#42,#7" {
		t.Fatalf("labels or issue references are incorrect: %+v", intent)
	}
	if len(intent.RepositoryGuidance) != 1 || intent.RepositoryGuidance[0].Path != "AGENTS.md" {
		t.Fatalf("repository guidance was not loaded: %+v", intent.RepositoryGuidance)
	}
}

func TestBuildChangeIntentRejectsSymlinkedGuidance(t *testing.T) {
	repository := t.TempDir()
	external := filepath.Join(t.TempDir(), "outside.md")
	writeContextFile(t, external, "outside")
	if err := os.Symlink(external, filepath.Join(repository, "AGENTS.md")); err != nil {
		t.Skipf("create symlink: %v", err)
	}
	intent, warnings := BuildChangeIntent(repository, "", 1024)
	if len(intent.RepositoryGuidance) != 0 || len(warnings) != 1 || !strings.Contains(warnings[0], "symbolic links") {
		t.Fatalf("symlinked guidance was not rejected: intent=%+v warnings=%v", intent, warnings)
	}
}

func TestBuilderAttachesIntentWhenNoGoPackagesAreSelected(t *testing.T) {
	repository := t.TempDir()
	writeContextFile(t, filepath.Join(repository, "CONTRIBUTING.md"), "Preserve API compatibility.")
	bundle, err := NewBuilder(contextRunner{}).Build(context.Background(), Input{Repository: repository})
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Intent.Source != "repository_guidance" || len(bundle.Intent.RepositoryGuidance) != 1 {
		t.Fatalf("intent was not attached to empty package context: %+v", bundle)
	}
}
