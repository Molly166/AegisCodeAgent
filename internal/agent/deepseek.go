package agent

import (
	"net/http"
	"time"
)

const DefaultDeepSeekBaseURL = "https://api.deepseek.com"

// DeepSeekConfig and constructor remain source-compatible with pre-v1 callers.
type DeepSeekConfig struct {
	APIKey              string
	BaseURL             string
	HTTPClient          *http.Client
	AllowInsecure       bool
	AllowCustomEndpoint bool
	RequestTimeout      time.Duration
	MaxRetries          *int
}

type DeepSeekProvider struct{ *OpenAICompatibleProvider }

func NewDeepSeekProvider(configuration DeepSeekConfig) (*DeepSeekProvider, error) {
	provider, err := NewOpenAICompatibleProvider(ProviderConfig{
		Name: ProviderDeepSeek, APIKey: configuration.APIKey, BaseURL: configuration.BaseURL,
		HTTPClient: configuration.HTTPClient, AllowInsecure: configuration.AllowInsecure,
		AllowCustomEndpoint: configuration.AllowCustomEndpoint, RequestTimeout: configuration.RequestTimeout,
		MaxRetries: configuration.MaxRetries,
	})
	if err != nil {
		return nil, err
	}
	return &DeepSeekProvider{provider}, nil
}
