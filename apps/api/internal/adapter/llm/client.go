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
		client:  &http.Client{Timeout: requestTimeout},
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
	// ReasoningContent is what reasoning models emit alongside (or instead of)
	// Content. Read-only, for the truncation error above.
	ReasoningContent string `json:"reasoning_content"`
}

// requestTimeout bounds one completion.
//
// This is not a "slow network" allowance. The configured gateway fronts a
// REASONING model, which emits its thinking before its answer and does not
// stream, so the client waits for the whole thing in silence: measured wall
// clock for the same three short comments ranges 3.8s to beyond 60s, varying
// per call, not per prompt size. At 60s the tail of that distribution was cut
// off and surfaced as `llm: call: ... context deadline exceeded`, which the
// dashboard shows as a failed generation — the draft was usually fine, it just
// arrived late. Sized so the observed tail lands with room to spare.
const requestTimeout = 180 * time.Second

// maxTokens is the generation ceiling for one completion. Sized for reasoning
// models: the budget covers `reasoning_content` BEFORE any answer tokens, so a
// value sized for the answer alone (a few 1-2 line comments ≈ 80 tokens) on a
// reasoning model can be spent entirely on thinking. When that happens the
// choices come back with `content: null` and `finish_reason: "length"`, which
// the caller cannot distinguish from a genuinely empty completion. Measured
// against the configured gateway: 300 → null, 800 → a complete answer.
const maxTokens = 1500

// chatResponse carries only the fields read back.
type chatResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
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
		MaxTokens:   maxTokens,
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
	if err := json.Unmarshal(firstJSONValue(raw), &out); err != nil {
		return "", fmt.Errorf("llm: decode: %w", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("llm: api error: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("llm: empty completion")
	}
	content := strings.TrimSpace(out.Choices[0].Message.Content)
	// A reasoning model can spend the whole budget thinking and return an empty
	// answer. Say that plainly — the caller's "empty output" path reads as a
	// screening problem otherwise, and the fix is a bigger budget, not a
	// different prompt.
	if content == "" && out.Choices[0].FinishReason == "length" {
		return "", fmt.Errorf("llm: no answer within the token budget (finish_reason=length, %d reasoning chars)", len(out.Choices[0].Message.ReasoningContent))
	}
	return content, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// firstJSONValue returns the leading JSON value of body, ignoring anything
// after it.
//
// Some gateways answer a NON-streaming request with a streaming-shaped body:
// one complete JSON object followed by SSE terminator lines ("data: [DONE]").
// The payload is correct — it is the same object the non-streaming shape
// carries — so plain json.Unmarshal, which rejects trailing data, fails with
// `invalid character 'd' after top-level value` and every AI draft 502s against
// such a gateway. json.Decoder reads one value and stops, which is exactly the
// tolerance needed, and a genuinely truncated body still errors.
func firstJSONValue(body []byte) []byte {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return body
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return body
	}
	return raw
}
