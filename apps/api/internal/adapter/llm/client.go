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
	"regexp"
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

// maxTokensCap is the hard ceiling the client will ever send as max_tokens.
// It is a CLAMP, not a target: the caller (service) sizes the completion
// budget to how much output it asked for, and the client only refuses to
// exceed this cap. A reasoning model spends its `reasoning_content` BEFORE
// any answer tokens, so a budget sized for the answer alone can be spent
// entirely on thinking — the choices then come back with `content: null` and
// `finish_reason: "length"`, which the caller cannot distinguish from a
// genuinely empty completion. The cap is the largest the configured gateway
// accepts; the per-request budget is always well below it, so it only guards
// a caller bug, never a real request.
const maxTokensCap = 16384

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
// maxTokens is the caller's completion budget; it is clamped to the gateway's
// hard cap so a caller bug cannot ask for more than the gateway accepts.
func (c *Client) Complete(ctx context.Context, system, user string, temperature float64, maxTokens int) (string, error) {
	if !c.Available() {
		return "", fmt.Errorf("llm: client is not configured")
	}
	if maxTokens > maxTokensCap {
		maxTokens = maxTokensCap
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
	if looksLikeSSE(raw) {
		content, err := parseSSE(raw)
		if err != nil {
			return "", err
		}
		return content, nil
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

// looksLikeSSE reports whether the gateway answered with a real SSE stream
// (many "data: " chunks) rather than a single JSON object. Some gateways
// stream certain models even for non-streaming requests; the payload is the
// same completion, chunked.
func looksLikeSSE(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	return len(trimmed) > 0 && strings.HasPrefix(string(trimmed), "data: ")
}

// sseChunk is one event of a streamed chat completion.
type sseChunk struct {
	Choices []struct {
		Delta        chatMessage `json:"delta"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
}

// thinkingBlock strips reasoning models that emit their thinking inline in the
// content as <thinking>...</thinking> instead of a separate
// reasoning_content field. A thinking block is never answer material.
var thinkingBlock = regexp.MustCompile(`(?s)<thinking>.*?</thinking>`)

// parseSSE reassembles a streamed completion: it concatenates the delta
// content across events, drops inline thinking blocks, and errors on a
// gateway error event or an event-less body.
func parseSSE(raw []byte) (string, error) {
	var b strings.Builder
	var finish string
	sawEvent := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var ev struct {
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(payload), &ev); err == nil && ev.Error != nil {
			return "", fmt.Errorf("llm: api error: %s", ev.Error.Message)
		}
		var ch sseChunk
		if err := json.Unmarshal([]byte(payload), &ch); err != nil {
			return "", fmt.Errorf("llm: decode stream event: %w", err)
		}
		for _, choice := range ch.Choices {
			b.WriteString(choice.Delta.Content)
			if choice.FinishReason != "" {
				finish = choice.FinishReason
			}
		}
		sawEvent = true
	}
	if !sawEvent {
		return "", fmt.Errorf("llm: empty stream")
	}
	content := strings.TrimSpace(thinkingBlock.ReplaceAllString(b.String(), ""))
	if content == "" && finish == "length" {
		return "", fmt.Errorf("llm: no answer within the token budget (finish_reason=length)")
	}
	return content, nil
}
