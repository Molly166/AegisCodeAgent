package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

const (
	DefaultDeepSeekBaseURL = "https://api.deepseek.com"
	maxAPIResponseBytes    = 4 * 1024 * 1024
)

type DeepSeekConfig struct {
	APIKey              string
	BaseURL             string
	HTTPClient          *http.Client
	AllowInsecure       bool
	AllowCustomEndpoint bool
}

type DeepSeekProvider struct {
	apiKey     string
	endpoint   string
	httpClient *http.Client
}

func NewDeepSeekProvider(configuration DeepSeekConfig) (*DeepSeekProvider, error) {
	configuration.APIKey = strings.TrimSpace(configuration.APIKey)
	if configuration.APIKey == "" {
		return nil, errors.New("DeepSeek API key is required")
	}
	baseURL := strings.TrimSpace(configuration.BaseURL)
	if baseURL == "" {
		baseURL = DefaultDeepSeekBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DeepSeek base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("DeepSeek base URL must be an absolute URL without credentials, query, or fragment")
	}
	if parsed.Scheme != "https" && !configuration.AllowInsecure {
		return nil, errors.New("DeepSeek base URL must use HTTPS")
	}
	if parsed.Host != "api.deepseek.com" && !configuration.AllowCustomEndpoint {
		return nil, errors.New("custom DeepSeek endpoints require explicit opt-in")
	}
	client := configuration.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Minute}
	}
	endpoint := strings.TrimRight(parsed.String(), "/") + "/chat/completions"
	return &DeepSeekProvider{apiKey: configuration.APIKey, endpoint: endpoint, httpClient: client}, nil
}

func (p *DeepSeekProvider) Name() string { return ProviderDeepSeek }

func (p *DeepSeekProvider) Complete(ctx context.Context, request CompletionRequest) (CompletionResponse, error) {
	if strings.TrimSpace(request.Model) == "" {
		return CompletionResponse{}, errors.New("DeepSeek model is required")
	}
	if request.MaxOutputTokens <= 0 {
		return CompletionResponse{}, errors.New("DeepSeek max output tokens must be positive")
	}
	payload := map[string]any{
		"model":      request.Model,
		"messages":   deepSeekMessages(request.Messages),
		"max_tokens": request.MaxOutputTokens,
		"thinking": map[string]any{
			"type": map[bool]string{true: "enabled", false: "disabled"}[request.Thinking],
		},
	}
	if request.Thinking {
		payload["reasoning_effort"] = request.ReasoningEffort
	}
	if request.JSONOutput {
		payload["response_format"] = map[string]string{"type": "json_object"}
	}
	if len(request.Tools) > 0 {
		tools := make([]map[string]any, 0, len(request.Tools))
		for _, definition := range request.Tools {
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": definition.Name, "description": definition.Description, "parameters": definition.Parameters,
				},
			})
		}
		payload["tools"] = tools
		payload["tool_choice"] = "auto"
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("encode DeepSeek request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(encoded))
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("create DeepSeek request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("User-Agent", "AegisCodeAgent/0.6")

	httpResponse, err := p.httpClient.Do(httpRequest)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("send DeepSeek request: %w", err)
	}
	defer httpResponse.Body.Close()
	body, err := io.ReadAll(io.LimitReader(httpResponse.Body, maxAPIResponseBytes+1))
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("read DeepSeek response: %w", err)
	}
	if len(body) > maxAPIResponseBytes {
		return CompletionResponse{}, fmt.Errorf("DeepSeek response exceeds %d bytes", maxAPIResponseBytes)
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		return CompletionResponse{}, decodeDeepSeekError(httpResponse.StatusCode, body)
	}
	var decoded deepSeekResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return CompletionResponse{}, fmt.Errorf("decode DeepSeek response: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return CompletionResponse{}, errors.New("DeepSeek response contains no choices")
	}
	choice := decoded.Choices[0]
	message := Message{
		Role:             "assistant",
		Content:          stringValue(choice.Message.Content),
		ReasoningContent: stringValue(choice.Message.ReasoningContent),
		ToolCalls:        make([]ToolCall, 0, len(choice.Message.ToolCalls)),
	}
	for _, toolCall := range choice.Message.ToolCalls {
		message.ToolCalls = append(message.ToolCalls, ToolCall{
			ID: toolCall.ID, Name: toolCall.Function.Name, Arguments: append(json.RawMessage(nil), []byte(toolCall.Function.Arguments)...),
		})
	}
	usage := review.AgentUsage{
		PromptTokens: decoded.Usage.PromptTokens, CompletionTokens: decoded.Usage.CompletionTokens,
		ReasoningTokens: decoded.Usage.CompletionTokensDetails.ReasoningTokens, TotalTokens: decoded.Usage.TotalTokens,
	}
	return CompletionResponse{Model: decoded.Model, Message: message, FinishReason: choice.FinishReason, Usage: usage}, nil
}

func deepSeekMessages(messages []Message) []map[string]any {
	result := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		converted := map[string]any{"role": message.Role, "content": message.Content}
		if message.Role == "assistant" {
			if message.Content == "" {
				converted["content"] = nil
			}
			if message.ReasoningContent != "" {
				converted["reasoning_content"] = message.ReasoningContent
			}
			if len(message.ToolCalls) > 0 {
				calls := make([]map[string]any, 0, len(message.ToolCalls))
				for _, call := range message.ToolCalls {
					calls = append(calls, map[string]any{
						"id": call.ID, "type": "function",
						"function": map[string]any{"name": call.Name, "arguments": string(call.Arguments)},
					})
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

type deepSeekResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content          *string `json:"content"`
			ReasoningContent *string `json:"reasoning_content"`
			ToolCalls        []struct {
				ID       string `json:"id"`
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

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func decodeDeepSeekError(status int, body []byte) error {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Error.Message != "" {
		detail := envelope.Error.Message
		if envelope.Error.Code != "" {
			detail += " (" + envelope.Error.Code + ")"
		} else if envelope.Error.Type != "" {
			detail += " (" + envelope.Error.Type + ")"
		}
		return fmt.Errorf("DeepSeek API returned HTTP %d: %s", status, truncateText(detail, 600))
	}
	return fmt.Errorf("DeepSeek API returned HTTP %d", status)
}
