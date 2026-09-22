// Package llm is the OpenAI-compatible chat client behind AI comment
// generation. It is the only place that knows the wire shape of a chat
// completion; the service layer works in domain terms only.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to any OpenAI-compatible /v1/chat/completions endpoint
// (OpenAI, local gateways, vLLM, ...).
type Client struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// Config wires the client. An empty BaseURL or APIKey yields an invalid
// client — the service checks Available() and degrades instead of dialing.
func New(baseURL, apiKey, model string) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:  strings.TrimSpace(apiKey),
		model:   strings.TrimSpace(model),
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

// Available reports whether the client has enough config to attempt a call.
func (c *Client) Available() bool {
	return c != nil && c.baseURL != "" && c.apiKey != "" && c.model != ""
}

// chatRequest is the subset of the OpenAI chat schema the generator uses.
type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatResponse carries only the fields read back.
type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Complete sends one chat completion and returns the assistant message.
func (c *Client) Complete(ctx context.Context, system, user string, temperature float64) (string, error) {
	if !c.Available() {
		return "", fmt.Errorf("llm: client is not configured")
	}
	body, err := json.Marshal(chatRequest{
		Model:       c.model,
		Messages:    []chatMessage{{Role: "system", Content: system}, {Role: "user", Content: user}},
		Temperature: temperature,
		MaxTokens:   300,
	})
	if err != nil {
		return "", fmt.Errorf("llm: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("llm: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: call: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("llm: read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("llm: http %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}
	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("llm: decode: %w", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("llm: api error: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("llm: empty completion")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
