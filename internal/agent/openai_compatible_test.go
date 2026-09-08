package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

const cleanCompletion = `{"id":"completion-1","model":"resolved-model","choices":[{"finish_reason":"stop","message":{"content":"{\"summary\":\"No candidate defects found.\",\"candidates\":[]}"}}],"usage":{"prompt_tokens":20,"completion_tokens":10,"total_tokens":30}}`

func localProvider(t *testing.T, server *httptest.Server, name string, modify func(*ProviderConfig)) *OpenAICompatibleProvider {
	t.Helper()
	configuration := ProviderConfig{Name: name, APIKey: "test-provider-secret", BaseURL: server.URL, AllowInsecure: true, AllowCustomEndpoint: true, Model: "test-model"}
	if modify != nil {
		modify(&configuration)
	}
	provider, err := NewOpenAICompatibleProvider(configuration)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func minimalRequest() CompletionRequest {
	return CompletionRequest{Model: "test-model", MaxOutputTokens: 512, Messages: []Message{{Role: "user", Content: "private-code-prompt"}}}
}

func TestCompatibleProviderRetriesRateLimitsAndRecordsFinalRoute(t *testing.T) {
	var calls atomic.Int32
	var firstBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		if request.URL.Path != "/chat/completions" || request.Header.Get("Authorization") != "Bearer test-provider-secret" {
			t.Error("invalid authenticated endpoint request")
		}
		if calls.Add(1) == 1 {
			firstBody = body
			writer.Header().Set("Retry-After", "0")
			writer.Header().Set("X-Orca-Request-Id", "failed-request")
			writer.Header().Set("X-Orca-Fallback-Model", "failed-model")
			writer.WriteHeader(429)
			io.WriteString(writer, `{"error":{"message":"test-provider-secret private-code-prompt"}}`)
			return
		}
		if string(body) != string(firstBody) {
			t.Error("retry payload changed")
		}
		writer.Header().Set("X-Orca-Request-Id", "orca-request-2")
		writer.Header().Set("X-Orca-Resolved-Model", "primary-model")
		writer.Header().Set("X-Orca-Fallback-Model", "fallback-model")
		writer.Header().Set("X-Orca-Fallback-Level", "1")
		io.WriteString(writer, cleanCompletion)
	}))
	defer server.Close()
	provider := localProvider(t, server, ProviderOrcaRouter, nil)
	response, err := provider.Complete(context.Background(), minimalRequest())
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || response.Model != "fallback-model" || response.Metadata.RequestID != "orca-request-2" || response.Metadata.Attempts != 2 || response.Metadata.FallbackLevel != 1 || response.Metadata.HTTPStatus != 200 {
		t.Fatalf("incorrect retry metadata: %+v", response)
	}
	if response.Usage.TotalTokens != 30 {
		t.Fatalf("usage = %+v", response.Usage)
	}
}

func TestCompatibleProviderBoundsRetriesAndDoesNotEchoErrors(t *testing.T) {
	for _, status := range []int{400, 401, 403, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				writer.Header().Set("Retry-After", "0")
				writer.Header().Set("X-Request-ID", "safe-id")
				writer.WriteHeader(status)
				io.WriteString(writer, `{"error":{"message":"test-provider-secret private-code-prompt","code":"private-code-prompt"}}`)
			}))
			defer server.Close()
			provider := localProvider(t, server, ProviderOpenAICompatible, nil)
			response, err := provider.Complete(context.Background(), minimalRequest())
			var apiErr *ProviderError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != status {
				t.Fatalf("expected typed HTTP error, got %v", err)
			}
			if strings.Contains(err.Error(), "test-provider-secret") || strings.Contains(err.Error(), "private-code-prompt") {
				t.Fatalf("upstream error leaked: %v", err)
			}
			want := int32(1)
			if status == 429 || status == 503 {
				want = 3
			}
			if calls.Load() != want || response.Metadata.Attempts != int(want) || response.Metadata.ErrorKind == "" {
				t.Fatalf("calls=%d trace=%+v", calls.Load(), response.Metadata)
			}
		})
	}
}

func TestCompatibleProviderRejectsRedirectsWithoutMutatingClient(t *testing.T) {
	var leaked atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		leaked.Add(1)
		io.WriteString(writer, cleanCompletion)
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	var customRedirect atomic.Int32
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { customRedirect.Add(1); return nil }}
	provider := localProvider(t, origin, ProviderOpenAICompatible, func(configuration *ProviderConfig) { configuration.HTTPClient = client })
	response, err := provider.Complete(context.Background(), minimalRequest())
	if err == nil || response.Metadata.ErrorKind != "redirect_rejected" || leaked.Load() != 0 || customRedirect.Load() != 0 {
		t.Fatalf("redirect safety failed: error=%v trace=%+v leaked=%d", err, response.Metadata, leaked.Load())
	}
	client.CheckRedirect(nil, nil)
	if customRedirect.Load() != 1 {
		t.Fatal("injected client was mutated")
	}
}

func TestCompatibleProviderDeadlineAndCancellationInterruptBackoff(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Retry-After", "30")
		writer.WriteHeader(429)
	}))
	defer server.Close()
	provider := localProvider(t, server, ProviderOpenAICompatible, func(configuration *ProviderConfig) { configuration.RequestTimeout = 30 * time.Millisecond })
	started := time.Now()
	response, err := provider.Complete(context.Background(), minimalRequest())
	if !errors.Is(err, context.DeadlineExceeded) || response.Metadata.ErrorKind != "timeout" || time.Since(started) > time.Second {
		t.Fatalf("deadline was not honored: %v %+v", err, response.Metadata)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response, err = provider.Complete(ctx, minimalRequest())
	if !errors.Is(err, context.Canceled) || response.Metadata.Attempts != 0 {
		t.Fatalf("canceled request performed work: %v %+v", err, response.Metadata)
	}
}

func TestCompatibleProviderRejectsUnboundedRetryAfter(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writer.Header().Set("Retry-After", "86400")
		writer.WriteHeader(503)
	}))
	defer server.Close()
	_, err := localProvider(t, server, ProviderOpenAICompatible, nil).Complete(context.Background(), minimalRequest())
	if err == nil || calls.Load() != 1 {
		t.Fatalf("unbounded retry wait accepted: %v, calls=%d", err, calls.Load())
	}
	now := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	if delay, valid := retryDelay(now.Add(5*time.Second).Format(http.TimeFormat), 0, now); !valid || delay != 5*time.Second {
		t.Fatalf("HTTP date Retry-After = %s %v", delay, valid)
	}
	if _, valid := retryDelay("9223372036854775807", 0, now); valid {
		t.Fatal("overflow Retry-After accepted")
	}
}

func TestCompatibleProviderDoesNotRetryUncertainNetworkSend(t *testing.T) {
	var calls int
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("transport at https://test-provider-secret@example.com private-code-prompt")
	})}
	provider, err := NewOpenAICompatibleProvider(ProviderConfig{Name: ProviderOpenAICompatible, Model: "test-model", APIKey: "test-provider-secret", BaseURL: "https://endpoint.example/v1", AllowCustomEndpoint: true, HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	response, err := provider.Complete(context.Background(), minimalRequest())
	if err == nil || calls != 1 || response.Metadata.ErrorKind != "transport" || strings.Contains(err.Error(), "private-code") || strings.Contains(err.Error(), "test-provider-secret") {
		t.Fatalf("uncertain transport was retried or leaked: %v, calls=%d", err, calls)
	}
}

func TestCompatibleProviderCapabilitiesAreModelScoped(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		json.NewDecoder(request.Body).Decode(&payload)
		io.WriteString(writer, cleanCompletion)
	}))
	defer server.Close()
	provider := localProvider(t, server, ProviderOpenAICompatible, nil)
	request := minimalRequest()
	request.Messages = append(request.Messages, Message{Role: "assistant", ReasoningContent: "private reasoning"})
	if _, err := provider.Complete(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"tools", "tool_choice", "response_format", "thinking", "reasoning_effort"} {
		if _, exists := payload[field]; exists {
			t.Errorf("unconfigured capability %s sent", field)
		}
	}
	if strings.Contains(string(mustMarshal(t, payload)), "private reasoning") {
		t.Error("reasoning replay occurred without capability")
	}
	request.JSONOutput = true
	if _, err := provider.Complete(context.Background(), request); err == nil {
		t.Fatal("unconfigured JSON capability accepted")
	}
	request.JSONOutput, request.Thinking = false, true
	if _, err := provider.Complete(context.Background(), request); err == nil {
		t.Fatal("unconfigured thinking capability accepted")
	}
	request.Thinking = false
	request.Model = "another-model"
	if _, err := provider.Complete(context.Background(), request); err == nil {
		t.Fatal("capabilities escaped configured model")
	}
}

func TestOrcaPresetUsesCommonReasoningProtocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		json.NewDecoder(request.Body).Decode(&payload)
		if _, exists := payload["thinking"]; exists {
			t.Error("DeepSeek-only field sent to gateway")
		}
		if payload["reasoning_effort"] != "high" || payload["response_format"] == nil || payload["tools"] == nil {
			t.Errorf("missing configured capability: %+v", payload)
		}
		io.WriteString(writer, cleanCompletion)
	}))
	defer server.Close()
	provider := localProvider(t, server, ProviderOrcaRouter, func(configuration *ProviderConfig) { configuration.Model = "deepseek/deepseek-v4-flash" })
	request := minimalRequest()
	request.Model, request.Thinking, request.JSONOutput, request.ReasoningEffort = "deepseek/deepseek-v4-flash", true, true, "high"
	request.Tools = []ToolDefinition{{Name: "read_file_lines", Parameters: map[string]any{"type": "object"}}}
	if _, err := provider.Complete(context.Background(), request); err != nil {
		t.Fatal(err)
	}
}

func TestCompatibleProviderExplicitCapabilityOverride(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		json.NewDecoder(request.Body).Decode(&payload)
		if payload["max_completion_tokens"] != float64(512) || payload["max_tokens"] != nil || payload["reasoning_effort"] != "high" {
			t.Errorf("incorrect explicit capability payload: %+v", payload)
		}
		io.WriteString(writer, cleanCompletion)
	}))
	defer server.Close()
	provider := localProvider(t, server, ProviderOpenAICompatible, func(configuration *ProviderConfig) {
		configuration.Capabilities = &ModelCapabilities{Reasoning: "effort", TokenLimitField: "max_completion_tokens"}
	})
	request := minimalRequest()
	request.Thinking, request.ReasoningEffort = true, "high"
	if _, err := provider.Complete(context.Background(), request); err != nil {
		t.Fatal(err)
	}
}

func TestCompatibleProviderProtocolFailureAndSizeLimits(t *testing.T) {
	for name, body := range map[string]string{
		"malformed":         `{"secret":"test-provider-secret"`,
		"no choices":        `{"choices":[]}`,
		"refusal":           `{"choices":[{"message":{"refusal":"private-code-prompt"}}]}`,
		"invalid tool args": `{"choices":[{"message":{"tool_calls":[{"id":"1","function":{"name":"read_file_lines","arguments":"not JSON"}}]}}]}`,
		"negative usage":    `{"choices":[{"message":{"content":"ok"}}],"usage":{"total_tokens":-1}}`,
		"oversize":          strings.Repeat("x", maxAPIResponseBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { io.WriteString(writer, body) }))
			defer server.Close()
			_, err := localProvider(t, server, ProviderOpenAICompatible, nil).Complete(context.Background(), minimalRequest())
			if err == nil || strings.Contains(err.Error(), "test-provider-secret") || strings.Contains(err.Error(), "private-code-prompt") {
				t.Fatalf("invalid protocol response accepted/leaked: %v", err)
			}
		})
	}
}

func TestProviderEndpointAndConfigurationValidation(t *testing.T) {
	for _, endpoint := range []string{"https://secret@example.com/v1", "https://api.deepseek.com?secret=x", "https://api.deepseek.com/v1#x", "https://api.deepseek.com/../v1", "ftp://api.deepseek.com", "https://api.deepseek.com.evil.example", "https://api.deepseek.com:8443"} {
		_, err := NewProvider(ProviderConfig{Name: ProviderDeepSeek, APIKey: "secret", BaseURL: endpoint})
		if err == nil {
			t.Errorf("unsafe endpoint accepted: %s", endpoint)
		}
		if err != nil && strings.Contains(err.Error(), "secret") {
			t.Errorf("URL credentials leaked: %v", err)
		}
	}
	for _, name := range []string{ProviderDeepSeek, ProviderOrcaRouter, ProviderOpenAICompatible, ProviderNone} {
		if err := ValidateProvider(name); err != nil {
			t.Error(err)
		}
	}
	for _, retries := range []int{-1, 6} {
		if _, err := NewProvider(ProviderConfig{Name: ProviderDeepSeek, APIKey: "secret", MaxRetries: &retries}); err == nil {
			t.Errorf("invalid retries %d accepted", retries)
		}
	}
	if _, err := NewProvider(ProviderConfig{Name: ProviderOpenAICompatible, APIKey: "secret", BaseURL: "https://endpoint.example/v1", AllowCustomEndpoint: true}); err == nil {
		t.Fatal("generic model not required")
	}
	for _, capabilities := range []ModelCapabilities{{Reasoning: "arbitrary"}, {TokenLimitField: "inject"}} {
		if _, err := normalizeCapabilities(capabilities); err == nil {
			t.Errorf("invalid capabilities accepted: %+v", capabilities)
		}
	}
	if defaults := DefaultProviderSettings(ProviderOrcaRouter); defaults.APIKeyEnv != "ORCAROUTER_API_KEY" || defaults.BaseURL != DefaultOrcaRouterBaseURL || defaults.Model != "deepseek/deepseek-v4-flash" {
		t.Fatalf("incorrect Orca defaults: %+v", defaults)
	}
}

func TestCompatibleProviderToolConversationRoundTrip(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		json.NewDecoder(request.Body).Decode(&payload)
		if calls.Add(1) == 1 {
			io.WriteString(writer, `{"model":"test-model","choices":[{"finish_reason":"tool_calls","message":{"reasoning_content":"not persisted","tool_calls":[{"id":"call-1","type":"function","function":{"name":"read_file_lines","arguments":"{\"path\":\"worker.go\",\"start_line\":1,\"end_line\":3}"}}]}}]}`)
			return
		}
		messages := payload["messages"].([]any)
		if messages[len(messages)-1].(map[string]any)["tool_call_id"] != "call-1" {
			t.Error("tool result lost call identity")
		}
		if messages[len(messages)-2].(map[string]any)["reasoning_content"] != "not persisted" {
			t.Error("configured reasoning replay missing")
		}
		writer.Header().Set("X-Request-ID", "second-request")
		io.WriteString(writer, cleanCompletion)
	}))
	defer server.Close()
	provider := localProvider(t, server, ProviderOpenAICompatible, func(configuration *ProviderConfig) {
		configuration.Capabilities = &ModelCapabilities{ToolCalling: true, JSONOutput: true, ReplayReasoning: true}
	})
	result, err := NewRunner(provider, &countingTools{}).Run(context.Background(), Config{Repository: "/tmp/repo", Model: "test-model"}, fixtureRunInput())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != review.AgentComplete || result.Steps != 2 || len(result.Completions) != 2 || result.Completions[1].RequestID != "second-request" || result.RequestedModel != "test-model" {
		t.Fatalf("unexpected trace: %+v", result)
	}
	if strings.Contains(string(mustMarshal(t, result)), "not persisted") {
		t.Error("private reasoning persisted")
	}
}

func TestCompatibleProviderBudgetExcludesReasoningThatIsNotReplayed(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		if calls.Add(1) == 1 {
			io.WriteString(writer, `{"model":"test-model","choices":[{"finish_reason":"tool_calls","message":{"reasoning_content":"`+strings.Repeat("x", 32*1024)+`","tool_calls":[{"id":"call-1","function":{"name":"read_file_lines","arguments":"{}"}}]}}]}`)
			return
		}
		if len(body) > 16*1024 || strings.Contains(string(body), "reasoning_content") {
			t.Error("nonreplayed reasoning was sent")
		}
		io.WriteString(writer, cleanCompletion)
	}))
	defer server.Close()
	provider := localProvider(t, server, ProviderOpenAICompatible, func(configuration *ProviderConfig) {
		configuration.Capabilities = &ModelCapabilities{ToolCalling: true, JSONOutput: true, ReplayReasoning: false}
	})
	result, err := NewRunner(provider, &countingTools{}).Run(context.Background(), Config{Repository: "/tmp/repo", Model: "test-model", MaxInputBytes: 16 * 1024}, fixtureRunInput())
	if err != nil || result.Status != review.AgentComplete || calls.Load() != 2 {
		t.Fatalf("discarded reasoning consumed the conversation budget: %+v %v", result, err)
	}
}

func TestCompatibleProviderUnknownModelCanReviewWithoutJSONMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { io.WriteString(writer, cleanCompletion) }))
	defer server.Close()
	provider := localProvider(t, server, ProviderOpenAICompatible, nil)
	result, err := NewRunner(provider, &countingTools{}).Run(context.Background(), Config{Repository: "/tmp/repo", Model: "test-model"}, fixtureRunInput())
	if err != nil || result.Status != review.AgentComplete || len(result.Warnings) != 2 {
		t.Fatalf("conservative model defaults unusable: %+v %v", result, err)
	}
	if !reflect.DeepEqual(provider.Capabilities("unconfigured"), ModelCapabilities{Reasoning: "none", TokenLimitField: "max_tokens"}) {
		t.Fatal("unknown model inherited capabilities")
	}
}

func TestCompatibleProviderDoesNotInventResolvedModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Request-ID", "test-provider-secret")
		io.WriteString(writer, `{"choices":[{"message":{"content":"{}"}}]}`)
	}))
	defer server.Close()
	response, err := localProvider(t, server, ProviderOpenAICompatible, nil).Complete(context.Background(), minimalRequest())
	if err != nil {
		t.Fatal(err)
	}
	if response.Model != "" || response.Metadata.ResolvedModel != "" || response.Metadata.RequestID != "" || response.Metadata.RequestedModel != "test-model" {
		t.Fatalf("invented model identity or leaked key: %+v", response.Metadata)
	}
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
