package config

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const maxConfigBytes = 64 * 1024

type File struct {
	Version  int            `json:"version"`
	Agent    AgentConfig    `json:"agent"`
	Verifier VerifierConfig `json:"verifier"`
}

type VerifierConfig struct {
	Enabled         *bool  `json:"enabled"`
	Timeout         string `json:"timeout"`
	AnalyzerTimeout string `json:"analyzer_timeout"`
}

type AgentConfig struct {
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	BaseURL         string `json:"base_url"`
	APIKeyEnv       string `json:"api_key_env"`
	Thinking        *bool  `json:"thinking"`
	ReasoningEffort string `json:"reasoning_effort"`
	Timeout         string `json:"timeout"`
	MaxSteps        int    `json:"max_steps"`
	MaxCandidates   int    `json:"max_candidates"`
	MaxInputBytes   int    `json:"max_input_bytes"`
	MaxOutputTokens int    `json:"max_output_tokens"`
}

func Load(path string) (File, error) {
	file, err := os.Open(path)
	if err != nil {
		return File{}, fmt.Errorf("open config %q: %w", path, err)
	}
	defer file.Close()

	decoder := json.NewDecoder(io.LimitReader(file, maxConfigBytes+1))
	decoder.DisallowUnknownFields()
	var result File
	if err := decoder.Decode(&result); err != nil {
		return File{}, fmt.Errorf("decode config %q: %w", path, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return File{}, fmt.Errorf("decode config %q: %w", path, err)
	}
	if result.Version != 1 {
		return File{}, fmt.Errorf("config %q uses unsupported version %d (supported: 1)", path, result.Version)
	}
	return result, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("multiple JSON values are not allowed")
}

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// APIKey reads a key from the process environment first, then from a local
// dotenv file. It never mutates the process environment and never returns
// values for keys other than the explicitly requested one.
func APIKey(dotEnvPath, name string) (string, error) {
	if !envNamePattern.MatchString(name) {
		return "", fmt.Errorf("invalid API key environment variable name %q", name)
	}
	if value, ok := os.LookupEnv(name); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value), nil
	}
	file, err := os.Open(dotEnvPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("open dotenv %q: %w", dotEnvPath, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(io.LimitReader(file, maxConfigBytes+1))
	scanner.Buffer(make([]byte, 1024), maxConfigBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, raw, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != name {
			continue
		}
		value, err := parseEnvValue(strings.TrimSpace(raw))
		if err != nil {
			return "", fmt.Errorf("parse %s in %q: %w", name, dotEnvPath, err)
		}
		return strings.TrimSpace(value), nil
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read dotenv %q: %w", dotEnvPath, err)
	}
	return "", nil
}

func parseEnvValue(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if value[0] == '\'' {
		if len(value) < 2 || value[len(value)-1] != '\'' {
			return "", errors.New("unterminated single-quoted value")
		}
		return value[1 : len(value)-1], nil
	}
	if value[0] == '"' {
		return strconv.Unquote(value)
	}
	if index := bytes.IndexByte([]byte(value), '#'); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	return value, nil
}
