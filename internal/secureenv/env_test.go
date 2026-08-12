package secureenv

import (
	"reflect"
	"testing"
)

func TestForUntrustedChildStripsCredentialsAndPreservesBuildEnvironment(t *testing.T) {
	input := []string{
		"PATH=/usr/bin", "GOCACHE=/tmp/go-cache", "GOPROXY=https://proxy.golang.org",
		"DEEPSEEK_API_KEY=deepseek-secret", "GITHUB_TOKEN=github-secret",
		"ACTIONS_RUNTIME_TOKEN=runtime-secret", "DATABASE_PASSWORD=password",
		"AWS_SECRET_ACCESS_KEY=aws-secret", "SERVICE_CREDENTIALS=credential",
		"AWS_ACCESS_KEY_ID=aws-id", "SSH_AUTH_SOCK=/tmp/agent.sock",
		"KUBECONFIG=/tmp/kubeconfig", "WEB_SESSION_ID=session",
	}
	want := []string{"PATH=/usr/bin", "GOCACHE=/tmp/go-cache", "GOPROXY=https://proxy.golang.org"}
	if got := ForUntrustedChild(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("ForUntrustedChild() = %#v, want %#v", got, want)
	}
}
