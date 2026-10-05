package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The gateway observed in production answers a NON-streaming request with a
// streaming-shaped body: one whole JSON object, then SSE terminator lines. The
// payload is the correct completion, but json.Unmarshal rejects the trailing
// bytes, so every AI draft 502'd with:
//
//	llm: decode: invalid character 'd' after top-level value
//
// This pins the tolerance so the fix is not silently reverted.
func TestCompleteToleratesTrailingSSETerminator(t *testing.T) {
	body := `{"choices":[{"message":{"role":"assistant","content":"Halo!"}}]}` + "\n\ndata: [DONE]\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	got, err := New(srv.URL, "k", "m").Complete(context.Background(), "sys", "usr", 0.7, 800)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "Halo!" {
		t.Fatalf("content = %q, want %q", got, "Halo!")
	}
}

// A plain OpenAI-shaped body must keep working.
func TestCompletePlainJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}]}`))
	}))
	defer srv.Close()

	got, err := New(srv.URL, "k", "m").Complete(context.Background(), "s", "u", 0.7, 800)
	if err != nil || got != "hi" {
		t.Fatalf("got %q, err %v", got, err)
	}
}

// A reasoning model that burns the whole budget thinking returns
// `content: null` with `finish_reason: "length"`. That must surface as a budget
// error, not as an empty completion the caller blames on its own screening.
func TestCompleteReasoningBudgetExhausted(t *testing.T) {
	body := `{"choices":[{"finish_reason":"length","message":{"role":"assistant","content":null,"reasoning_content":"Let me think about this..."}}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	_, err := New(srv.URL, "k", "m").Complete(context.Background(), "s", "u", 0.7, 800)
	if err == nil {
		t.Fatal("want error for budget-exhausted completion, got nil")
	}
	if !strings.Contains(err.Error(), "token budget") {
		t.Fatalf("err = %v, want a token-budget message", err)
	}
}

// A truncated JSON is still an error — the tolerance must not turn a broken body
// into an empty success.
func TestCompleteTruncatedStillFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hal`))
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "k", "m").Complete(context.Background(), "s", "u", 0.7, 800); err == nil {
		t.Fatal("want error for truncated body, got nil")
	}
}

// The gateway in front of the configured model does not stream, so the client
// waits for a whole reasoning pass in silence — measured 3.8s to beyond 60s for
// the same prompt. A per-request timeout tuned for a chat-shaped model cuts the
// tail off and reports a failed generation for a draft that was merely late.
func TestRequestTimeoutFitsAReasoningPass(t *testing.T) {
	if requestTimeout < 120*time.Second {
		t.Fatalf("requestTimeout = %s; a non-streaming reasoning model routinely exceeds it", requestTimeout)
	}
}

// The gateway streams some models (Claude) as a real SSE event stream even
// for non-streaming requests. The client must reassemble the delta content
// across events instead of failing to decode it as one JSON object.
func TestCompleteParsesSSEStream(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"Halo\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\" dunia\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	got, err := New(srv.URL, "k", "m").Complete(context.Background(), "s", "u", 0.7, 800)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "Halo dunia" {
		t.Fatalf("content = %q, want %q", got, "Halo dunia")
	}
}

// A reasoning model that streams its thinking inline in the content
// (<thinking>...</thinking>) must not leak it into the answer.
func TestCompleteStripsInlineThinking(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"<thinking>berpikir keras</thinking>\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"Jawaban akhir\"}}]}\n\n" +
		"data: [DONE]\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	got, err := New(srv.URL, "k", "m").Complete(context.Background(), "s", "u", 0.7, 800)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "Jawaban akhir" {
		t.Fatalf("content = %q, want %q", got, "Jawaban akhir")
	}
}

// An SSE stream with no usable events is an error, not an empty success.
func TestCompleteSSEWithoutEventsFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "k", "m").Complete(context.Background(), "s", "u", 0.7, 800); err == nil {
		t.Fatal("want error for an event-less stream, got nil")
	}
}
