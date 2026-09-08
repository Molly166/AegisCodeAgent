package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

const (
	maxAPIResponseBytes = 4 * 1024 * 1024
	maxAPIRequestBytes  = 4 * 1024 * 1024
	maxRetryDelay       = 30 * time.Second
)

// OpenAICompatibleProvider implements bounded, non-streaming Chat Completions.
// A complete JSON envelope is required before candidates can enter verification.
type OpenAICompatibleProvider struct {
	name           string
	apiKey         string
	endpoint       string
	model          string
	capabilities   *ModelCapabilities
	httpClient     *http.Client
	requestTimeout time.Duration
	maxRetries     int
}

func NewOpenAICompatibleProvider(configuration ProviderConfig) (*OpenAICompatibleProvider, error) {
	configuration.Name = strings.ToLower(strings.TrimSpace(configuration.Name))
	if configuration.Name == "" {
		configuration.Name = ProviderOpenAICompatible
	}
	if err := ValidateProvider(configuration.Name); err != nil {
		return nil, err
	}
	if configuration.Name == ProviderNone {
		return nil, errors.New("none is not an HTTP provider")
	}
	configuration.APIKey = strings.TrimSpace(configuration.APIKey)
	if configuration.APIKey == "" {
		return nil, errors.New(ProviderDisplayName(configuration.Name) + " API key is required")
	}
	for _, character := range configuration.APIKey {
		if character < 0x21 || character > 0x7e {
			return nil, errors.New("API key must contain printable ASCII without whitespace")
		}
	}
	if len(configuration.APIKey) > 4096 {
		return nil, errors.New("API key exceeds the supported length")
	}
	configuration.Model = strings.TrimSpace(configuration.Model)
	if configuration.Model != "" {
		if err := validateModel(configuration.Model); err != nil {
			return nil, err
		}
	}
	if configuration.Name == ProviderOpenAICompatible && configuration.Model == "" {
		return nil, errors.New("OpenAI-compatible provider requires an explicit model")
	}
	var capabilities *ModelCapabilities
	if configuration.Capabilities != nil {
		if configuration.Model == "" {
			return nil, errors.New("capability overrides require an explicit model")
		}
		normalized, err := normalizeCapabilities(*configuration.Capabilities)
		if err != nil {
			return nil, err
		}
		capabilities = &normalized
	}
	baseURL := strings.TrimSpace(configuration.BaseURL)
	if baseURL == "" {
		baseURL = DefaultProviderSettings(configuration.Name).BaseURL
	}
	endpoint, err := validateEndpoint(configuration.Name, baseURL, configuration.AllowInsecure, configuration.AllowCustomEndpoint)
	if err != nil {
		return nil, err
	}
	if configuration.RequestTimeout == 0 {
		configuration.RequestTimeout = 3 * time.Minute
	}
	if configuration.RequestTimeout < time.Millisecond || configuration.RequestTimeout > 10*time.Minute {
		return nil, errors.New("provider request timeout must be between 1ms and 10m")
	}
	maxRetries := 2
	if configuration.MaxRetries != nil {
		maxRetries = *configuration.MaxRetries
	}
	if maxRetries < 0 || maxRetries > 5 {
		return nil, errors.New("provider max retries must be between 0 and 5")
	}
	client := &http.Client{}
	if configuration.HTTPClient != nil {
		*client = *configuration.HTTPClient
	}
	// Do not mutate an injected client or permit redirect handlers to forward a
	// request body and API key to a different origin (including subdomains).
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Jar = nil
	return &OpenAICompatibleProvider{
		name: configuration.Name, apiKey: configuration.APIKey, endpoint: endpoint,
		model: configuration.Model, capabilities: capabilities, httpClient: client,
		requestTimeout: configuration.RequestTimeout, maxRetries: maxRetries,
	}, nil
}

func validateEndpoint(provider, baseURL string, allowInsecure, allowCustom bool) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", errors.New("provider base URL is invalid")
	}
	if parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", errors.New("provider base URL must be absolute without credentials, query, or fragment")
	}
	if parsed.Scheme != "https" && !(allowInsecure && parsed.Scheme == "http") {
		return "", errors.New("provider base URL must use HTTPS")
	}
	if parsed.RawPath != "" || strings.Contains(parsed.Path, "..") || strings.Contains(parsed.Path, "\\") {
		return "", errors.New("provider base URL contains an ambiguous path")
	}
	official := DefaultProviderSettings(provider).BaseURL
	if provider == ProviderOpenAICompatible || strings.TrimRight(parsed.String(), "/") != official {
		// The official DeepSeek API also documents /v1 as an interchangeable prefix.
		deepseekV1 := provider == ProviderDeepSeek && strings.TrimRight(parsed.String(), "/") == DefaultDeepSeekBaseURL+"/v1"
		if !deepseekV1 && !allowCustom {
			return "", errors.New("custom provider endpoints require explicit opt-in")
		}
	}
	return strings.TrimRight(parsed.String(), "/") + "/chat/completions", nil
}

func (p *OpenAICompatibleProvider) Name() string { return p.name }

func (p *OpenAICompatibleProvider) Capabilities(model string) ModelCapabilities {
	if p.capabilities != nil && model == p.model {
		return *p.capabilities
	}
	return defaultCapabilities(p.name, model)
}

func (p *OpenAICompatibleProvider) Complete(ctx context.Context, request CompletionRequest) (response CompletionResponse, resultErr error) {
	started := time.Now()
	response.Metadata.Provider = p.name
	response.Metadata.RequestedModel = p.safeMetadata(request.Model)
	defer func() {
		response.Metadata.DurationMillis = time.Since(started).Milliseconds()
		if resultErr != nil {
			var providerErr *ProviderError
			if errors.As(resultErr, &providerErr) {
				response.Metadata.ErrorKind = providerErr.Kind
			} else {
				response.Metadata.ErrorKind = "invalid_configuration"
			}
		}
	}()
	if err := validateModel(request.Model); err != nil {
		return response, err
	}
	if p.model != "" && p.model != request.Model {
		return response, errors.New("completion model differs from configured provider model")
	}
	if request.MaxOutputTokens <= 0 || request.MaxOutputTokens > 65536 {
		return response, errors.New("max output tokens must be between 1 and 65536")
	}
	capabilities := p.Capabilities(request.Model)
	if request.Thinking && capabilities.Reasoning == "none" {
		return response, errors.New("thinking requires an explicit reasoning capability for this model")
	}
	if request.Thinking {
		switch request.ReasoningEffort {
		case "minimal", "low", "medium", "high", "xhigh", "max":
		default:
			return response, errors.New("unsupported reasoning effort")
		}
	}
	if len(request.Tools) > 0 && !capabilities.ToolCalling {
		return response, errors.New("tool calling capability is not enabled for this model")
	}
	if request.JSONOutput && !capabilities.JSONOutput {
		return response, errors.New("JSON output capability is not enabled for this model")
	}
	payload := map[string]any{
		"model": request.Model, "messages": compatibleMessages(request.Messages, capabilities.ReplayReasoning),
		capabilities.TokenLimitField: request.MaxOutputTokens, "stream": false,
	}
	if capabilities.Reasoning == "deepseek" {
		payload["thinking"] = map[string]string{"type": map[bool]string{true: "enabled", false: "disabled"}[request.Thinking]}
	}
	if request.Thinking {
		payload["reasoning_effort"] = request.ReasoningEffort
	}
	if request.JSONOutput {
		payload["response_format"] = map[string]string{"type": "json_object"}
	}
	if len(request.Tools) > 0 {
		payload["tools"], payload["tool_choice"] = compatibleTools(request.Tools), "auto"
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return response, &ProviderError{Provider: p.name, Kind: "request_encoding"}
	}
	if len(encoded) > maxAPIRequestBytes {
		return response, &ProviderError{Provider: p.name, Kind: "request_too_large"}
	}
	// One completion timeout includes all attempts and backoff. A caller's whole
	// run deadline always wins, and cancellation interrupts backoff immediately.
	ctx, cancel := context.WithTimeout(ctx, p.requestTimeout)
	defer cancel()
	for attempt := 0; attempt <= p.maxRetries; attempt++ {
		if ctx.Err() != nil {
			return response, p.contextError(ctx.Err())
		}
		response.Metadata.Attempts = attempt + 1
		httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(encoded))
		if err != nil {
			return response, &ProviderError{Provider: p.name, Kind: "request_encoding"}
		}
		httpRequest.Header.Set("Authorization", "Bearer "+p.apiKey)
		httpRequest.Header.Set("Content-Type", "application/json")
		httpRequest.Header.Set("Accept", "application/json")
		httpRequest.Header.Set("User-Agent", "AegisCodeAgent/1.0")
		httpResponse, err := p.httpClient.Do(httpRequest)
		if err != nil {
			if ctx.Err() != nil {
				return response, p.contextError(ctx.Err())
			}
			// Transport failures can occur after an upstream accepted a billable
			// request. Retry only explicit 429/5xx responses, not uncertain sends.
			return response, &ProviderError{Provider: p.name, Kind: "transport"}
		}
		response.Metadata.HTTPStatus = httpResponse.StatusCode
		p.readMetadata(&response.Metadata, httpResponse.Header)
		body, readErr := io.ReadAll(io.LimitReader(httpResponse.Body, maxAPIResponseBytes+1))
		// Body read errors are handled below; closing a fully consumed HTTP body
		// cannot change the evidence bytes already obtained.
		_ = httpResponse.Body.Close()
		if readErr != nil {
			if ctx.Err() != nil {
				return response, p.contextError(ctx.Err())
			}
			return response, &ProviderError{Provider: p.name, Kind: "response_read"}
		}
		if len(body) > maxAPIResponseBytes {
			return response, &ProviderError{Provider: p.name, Kind: "response_too_large"}
		}
		if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
			retryable := httpResponse.StatusCode == 429 || httpResponse.StatusCode >= 500 && httpResponse.StatusCode <= 599
			apiErr := &ProviderError{Provider: p.name, Kind: classifyHTTPStatus(httpResponse.StatusCode), StatusCode: httpResponse.StatusCode, Retryable: retryable}
			delay, bounded := retryDelay(httpResponse.Header.Get("Retry-After"), attempt, time.Now())
			if !retryable || attempt == p.maxRetries || !bounded {
				return response, apiErr
			}
			if err := waitForRetry(ctx, delay); err != nil {
				return response, p.contextError(err)
			}
			continue
		}
		decoded, err := decodeCompletion(body)
		if err != nil {
			return response, &ProviderError{Provider: p.name, Kind: "invalid_response"}
		}
		response.Message, response.FinishReason, response.Usage = decoded.Message, decoded.FinishReason, decoded.Usage
		if response.Metadata.ResolvedModel == "" {
			response.Metadata.ResolvedModel = p.safeMetadata(decoded.Model)
		}
		response.Model = response.Metadata.ResolvedModel
		if response.Metadata.RequestID == "" {
			response.Metadata.RequestID = p.safeMetadata(decoded.Metadata.RequestID)
		}
		return response, nil
	}
	return response, &ProviderError{Provider: p.name, Kind: "retry_exhausted"}
}

func (p *OpenAICompatibleProvider) contextError(err error) error {
	kind := "canceled"
	if errors.Is(err, context.DeadlineExceeded) {
		kind = "timeout"
	}
	return &ProviderError{Provider: p.name, Kind: kind, cause: err}
}

func (p *OpenAICompatibleProvider) readMetadata(metadata *review.CompletionMetadata, headers http.Header) {
	// Only metadata from the last attempt is valid. Never carry fallback or a
	// request ID from a failed attempt into the succeeding attempt.
	metadata.RequestID, metadata.ResolvedModel, metadata.FallbackModel, metadata.FallbackLevel = "", "", "", 0
	if p.name == ProviderOrcaRouter {
		metadata.RequestID = p.safeMetadata(headers.Get("X-Orca-Request-Id"))
		metadata.ResolvedModel = p.safeMetadata(headers.Get("X-Orca-Resolved-Model"))
		metadata.FallbackModel = p.safeMetadata(headers.Get("X-Orca-Fallback-Model"))
		if level, err := strconv.Atoi(headers.Get("X-Orca-Fallback-Level")); err == nil && level >= 0 && level <= 100 {
			metadata.FallbackLevel = level
		}
		if metadata.FallbackModel != "" {
			metadata.ResolvedModel = metadata.FallbackModel
		}
	}
	if metadata.RequestID == "" {
		metadata.RequestID = p.safeMetadata(headers.Get("X-Request-Id"))
	}
}

func (p *OpenAICompatibleProvider) safeMetadata(value string) string {
	if len(value) > 192 || strings.Contains(value, p.apiKey) {
		return ""
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("-._/:@+", character)) {
			return ""
		}
	}
	return value
}

func retryDelay(header string, attempt int, now time.Time) (time.Duration, bool) {
	if header != "" {
		if seconds, err := strconv.ParseInt(strings.TrimSpace(header), 10, 64); err == nil {
			if seconds < 0 || seconds > int64(maxRetryDelay/time.Second) {
				return 0, false
			}
			return time.Duration(seconds) * time.Second, true
		}
		if date, err := http.ParseTime(header); err == nil {
			delay := date.Sub(now)
			if delay < 0 {
				delay = 0
			}
			return delay, delay <= maxRetryDelay
		}
	}
	delay := 250 * time.Millisecond * time.Duration(1<<attempt)
	return delay, delay <= maxRetryDelay
}

func waitForRetry(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func compatibleMessages(messages []Message, replayReasoning bool) []map[string]any {
	result := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		converted := map[string]any{"role": message.Role, "content": message.Content}
		if message.Role == "assistant" {
			if message.Content == "" {
				converted["content"] = nil
			}
			if replayReasoning && message.ReasoningContent != "" {
				converted["reasoning_content"] = message.ReasoningContent
			}
			if len(message.ToolCalls) > 0 {
				calls := make([]map[string]any, 0, len(message.ToolCalls))
				for _, call := range message.ToolCalls {
					calls = append(calls, map[string]any{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": string(call.Arguments)}})
				}
				converted["tool_calls"] = calls
			}
		}
		if message.Role == "tool" {
			converted["tool_call_id"] = message.ToolCallID
		}
		result = append(result, converted)
	}
	return result
}

func compatibleTools(definitions []ToolDefinition) []map[string]any {
	tools := make([]map[string]any, 0, len(definitions))
	for _, definition := range definitions {
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{
			"name": definition.Name, "description": definition.Description, "parameters": definition.Parameters,
		}})
	}
	return tools
}

func conversationInputBytes(messages []Message, definitions []ToolDefinition, replayReasoning bool) int {
	payload := map[string]any{"messages": compatibleMessages(messages, replayReasoning)}
	if len(definitions) > 0 {
		payload["tools"] = compatibleTools(definitions)
	}
	return encodedLength(payload)
}

type compatibleResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content          *string `json:"content"`
			ReasoningContent *string `json:"reasoning_content"`
			Refusal          *string `json:"refusal"`
			ToolCalls        []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens            int `json:"prompt_tokens"`
		CompletionTokens        int `json:"completion_tokens"`
		TotalTokens             int `json:"total_tokens"`
		CompletionTokensDetails struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage"`
}

func decodeCompletion(body []byte) (CompletionResponse, error) {
	var decoded compatibleResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return CompletionResponse{}, err
	}
	if len(decoded.Choices) != 1 {
		return CompletionResponse{}, errors.New("exactly one choice is required")
	}
	choice := decoded.Choices[0]
	switch choice.FinishReason {
	case "", "stop", "length", "tool_calls", "content_filter":
	default:
		return CompletionResponse{}, errors.New("invalid finish reason")
	}
	if choice.Message.Refusal != nil && *choice.Message.Refusal != "" {
		return CompletionResponse{}, errors.New("model refused the request")
	}
	if len(choice.Message.ToolCalls) > maxReturnedToolCallsPerStep {
		return CompletionResponse{}, errors.New("too many tool calls")
	}
	message := Message{Role: "assistant", Content: stringValue(choice.Message.Content), ReasoningContent: stringValue(choice.Message.ReasoningContent)}
	seen := make(map[string]bool)
	for _, call := range choice.Message.ToolCalls {
		if call.ID == "" || len(call.ID) > 160 || seen[call.ID] || call.Function.Name == "" || len(call.Function.Name) > 80 || (call.Type != "" && call.Type != "function") || len(call.Function.Arguments) > 64*1024 || !json.Valid([]byte(call.Function.Arguments)) {
			return CompletionResponse{}, errors.New("invalid tool call")
		}
		seen[call.ID] = true
		message.ToolCalls = append(message.ToolCalls, ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments)})
	}
	if message.Content == "" && len(message.ToolCalls) == 0 {
		return CompletionResponse{}, errors.New("empty model response")
	}
	usage := review.AgentUsage{PromptTokens: decoded.Usage.PromptTokens, CompletionTokens: decoded.Usage.CompletionTokens, TotalTokens: decoded.Usage.TotalTokens, ReasoningTokens: decoded.Usage.CompletionTokensDetails.ReasoningTokens}
	if usage.PromptTokens < 0 || usage.CompletionTokens < 0 || usage.TotalTokens < 0 || usage.ReasoningTokens < 0 {
		return CompletionResponse{}, errors.New("invalid token usage")
	}
	return CompletionResponse{Model: decoded.Model, Message: message, FinishReason: choice.FinishReason, Usage: usage, Metadata: review.CompletionMetadata{RequestID: decoded.ID}}, nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
