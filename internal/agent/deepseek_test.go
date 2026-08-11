package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestDeepSeekProviderUsesThinkingJSONAndToolProtocol(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/chat/completions" || request.Method != http.MethodPost {
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer test-secret" {
			t.Errorf("unexpected authorization header")
		}
		body, _ := io.ReadAll(request.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		thinking := payload["thinking"].(map[string]any)
		if thinking["type"] != "enabled" || payload["reasoning_effort"] != "high" {
			t.Errorf("thinking configuration missing: %s", body)
		}
		format := payload["response_format"].(map[string]any)
		if format["type"] != "json_object" {
			t.Errorf("JSON output missing: %s", body)
		}
		messages := payload["messages"].([]any)
		assistant := messages[1].(map[string]any)
		if assistant["reasoning_content"] != "preserved reasoning" {
			t.Errorf("reasoning content was not replayed: %s", body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"model":"deepseek-v4-flash","choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"reasoning_content":"next reasoning","tool_calls":[{"id":"call-1","type":"function","function":{"name":"read_file_lines","arguments":"{\"path\":\"main.go\",\"start_line\":1,\"end_line\":2}"}}]}}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18,"completion_tokens_details":{"reasoning_tokens":4}}}`)),
		}, nil
	})}

	provider, err := NewDeepSeekProvider(DeepSeekConfig{
		APIKey: "test-secret", BaseURL: "http://deepseek.test", HTTPClient: client, AllowInsecure: true, AllowCustomEndpoint: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := provider.Complete(context.Background(), CompletionRequest{
		Model: DefaultModel, Thinking: true, ReasoningEffort: "high", JSONOutput: true, MaxOutputTokens: 1024,
		Messages: []Message{{Role: "user", Content: "review"}, {Role: "assistant", ReasoningContent: "preserved reasoning", ToolCalls: []ToolCall{{ID: "previous", Name: "search_code", Arguments: json.RawMessage(`{}`)}}}},
		Tools:    []ToolDefinition{{Name: "read_file_lines", Parameters: map[string]any{"type": "object"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.ReasoningContent != "next reasoning" || len(response.Message.ToolCalls) != 1 || response.Usage.ReasoningTokens != 4 {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestDeepSeekProviderRejectsInsecureEndpointAndSanitizesError(t *testing.T) {
	if _, err := NewDeepSeekProvider(DeepSeekConfig{BaseURL: DefaultDeepSeekBaseURL}); err == nil {
		t.Fatal("missing credential was accepted")
	}
	if _, err := NewDeepSeekProvider(DeepSeekConfig{APIKey: "secret", BaseURL: "http://example.com"}); err == nil {
		t.Fatal("insecure endpoint was accepted")
	}
	if _, err := NewDeepSeekProvider(DeepSeekConfig{APIKey: "secret", BaseURL: "https://proxy.example.com"}); err == nil {
		t.Fatal("custom endpoint was accepted without opt-in")
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"invalid credential","code":"invalid_api_key"}}`)),
		}, nil
	})}
	provider, err := NewDeepSeekProvider(DeepSeekConfig{APIKey: "top-secret", BaseURL: "http://deepseek.test", HTTPClient: client, AllowInsecure: true, AllowCustomEndpoint: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Complete(context.Background(), CompletionRequest{Model: DefaultModel, MaxOutputTokens: 512})
	if err == nil || !strings.Contains(err.Error(), "invalid_api_key") || strings.Contains(err.Error(), "top-secret") {
		t.Fatalf("unexpected error: %v", err)
	}
}
