package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

const codexEncryptedContentRejectionEvent = `{"type":"error","error":{"type":"invalid_request_error","code":"invalid_encrypted_content","message":"The encrypted content for item rs_1 could not be verified. Reason: Encrypted content could not be decrypted or parsed."}}`

func codexRealmTestPayload(promptCacheKey string) []byte {
	valid := validCodexReasoningEncryptedContentForTest()
	return []byte(`{"model":"gpt-6-astra","stream":true,"prompt_cache_key":"` + promptCacheKey + `","input":[` +
		`{"id":"rs_1","type":"reasoning","encrypted_content":"` + valid + `","summary":[]},` +
		`{"type":"agent_message","id":"amsg_1","content":[{"type":"input_text","text":"report"},{"type":"encrypted_content","encrypted_content":"` + valid + `"}]},` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}` +
		`]}`)
}

// codexInputCarriesEncryptedContent looks only at input items: the request's
// standard include list names "reasoning.encrypted_content" on every call.
func codexInputCarriesEncryptedContent(body []byte) bool {
	for _, item := range gjson.GetBytes(body, "input").Array() {
		if item.Get("encrypted_content").Exists() {
			return true
		}
		for _, part := range item.Get("content").Array() {
			if part.Get("encrypted_content").Exists() {
				return true
			}
		}
	}
	return false
}

func codexRealmTestAuth(baseURL string) *cliproxyauth.Auth {
	return &cliproxyauth.Auth{ID: "auth-" + baseURL, Provider: "codex", Attributes: map[string]string{"base_url": baseURL, "api_key": "test"}}
}

func TestStripCodexEncryptedContent(t *testing.T) {
	body, stripped := stripCodexEncryptedContent(codexRealmTestPayload("thread-strip"))
	if stripped != 2 {
		t.Fatalf("stripped = %d, want 2", stripped)
	}
	if gjson.GetBytes(body, "input.0.encrypted_content").Exists() || gjson.GetBytes(body, "input.0.id").Exists() {
		t.Fatalf("reasoning item still carries ciphertext or a store id: %s", gjson.GetBytes(body, "input.0").Raw)
	}
	parts := gjson.GetBytes(body, "input.1.content").Array()
	if len(parts) != 1 || parts[0].Get("text").String() != "report" {
		t.Fatalf("agent message parts = %s, want only the plaintext part", gjson.GetBytes(body, "input.1.content").Raw)
	}
	if gjson.GetBytes(body, "input.2.content.0.text").String() != "hello" {
		t.Fatalf("user message changed: %s", gjson.GetBytes(body, "input.2").Raw)
	}
	if _, again := stripCodexEncryptedContent(body); again != 0 {
		t.Fatalf("second strip changed %d item(s), want 0", again)
	}
}

func TestCodexEncryptionRealmFromAuth(t *testing.T) {
	if got := codexEncryptionRealm(&cliproxyauth.Auth{Provider: "codex"}); got != codexEncryptionRealmDefault {
		t.Fatalf("subscription realm = %q, want %q", got, codexEncryptionRealmDefault)
	}
	if got := codexEncryptionRealm(codexRealmTestAuth("https://lekondo-codex-overflow.openai.azure.com/openai/v1")); got != "lekondo-codex-overflow.openai.azure.com" {
		t.Fatalf("azure realm = %q", got)
	}
}

// A thread that completed on one upstream and lands on another has its
// ciphertext stripped before the request leaves; the same upstream again
// keeps it.
func TestCodexExecutorStripsEncryptedContentWhenSessionChangesRealm(t *testing.T) {
	var bodies [][]byte
	newServer := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			bodies = append(bodies, body)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"completed\",\"output\":[]}}\n\n"))
		}))
	}
	first, second := newServer(), newServer()
	defer first.Close()
	defer second.Close()

	run := func(server *httptest.Server) {
		t.Helper()
		result, err := NewCodexExecutor(&config.Config{}).ExecuteStream(context.Background(), codexRealmTestAuth(server.URL), cliproxyexecutor.Request{
			Model: "gpt-6-astra", Payload: codexRealmTestPayload("thread-realm"),
		}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai-response"), Stream: true})
		if err != nil {
			t.Fatalf("ExecuteStream: %v", err)
		}
		for range result.Chunks {
		}
	}
	run(first)
	run(first)
	run(second)
	run(second)
	if len(bodies) != 4 {
		t.Fatalf("upstream calls = %d, want 4", len(bodies))
	}
	for i, want := range []bool{true, true, false, true} {
		if got := gjson.GetBytes(bodies[i], "input.0.encrypted_content").Exists(); got != want {
			t.Fatalf("call %d: reasoning ciphertext present = %v, want %v", i, got, want)
		}
	}
	if got := len(gjson.GetBytes(bodies[2], "input.1.content").Array()); got != 1 {
		t.Fatalf("call 2: agent message parts = %d, want 1", got)
	}
}

// When the upstream still rejects the ciphertext (no realm history), the
// auto executor retries once with every encrypted item removed and the
// client only ever sees the successful stream.
func TestCodexAutoExecutorRetriesStreamWithoutEncryptedContent(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		if codexInputCarriesEncryptedContent(body) {
			_, _ = w.Write([]byte("event: response.created\ndata: " + codexCreatedEvent + "\n\n"))
			_, _ = w.Write([]byte("event: error\ndata: " + codexEncryptedContentRejectionEvent + "\n\n"))
			return
		}
		if n != 2 {
			t.Errorf("stripped retry arrived as call %d, want 2", n)
		}
		_, _ = w.Write([]byte("event: response.created\ndata: " + codexCreatedEvent + "\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ok\",\"object\":\"response\",\"status\":\"completed\",\"output\":[]}}\n\n"))
	}))
	defer server.Close()

	result, err := NewCodexAutoExecutor(codexBufferingConfig(true)).ExecuteStream(context.Background(), codexRealmTestAuth(server.URL), cliproxyexecutor.Request{
		Model: "gpt-6-astra", Payload: codexRealmTestPayload("thread-retry"),
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai-response"), Stream: true})
	if err != nil {
		t.Fatalf("ExecuteStream: %v", err)
	}
	payloads, streamErr := drainChunks(result)
	if streamErr != nil {
		t.Fatalf("stream error reached the client: %v", streamErr)
	}
	if !strings.Contains(payloads, "resp_ok") {
		t.Fatalf("client did not receive the retried response: %s", payloads)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("upstream calls = %d, want 2", got)
	}
}

func TestCodexAutoExecutorRetriesNonStreamWithoutEncryptedContent(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		atomic.AddInt32(&calls, 1)
		if codexInputCarriesEncryptedContent(body) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","code":"invalid_encrypted_content","message":"The encrypted content for item rs_1 could not be verified."}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ok\",\"object\":\"response\",\"status\":\"completed\",\"output\":[]}}\n\n"))
	}))
	defer server.Close()

	resp, err := NewCodexAutoExecutor(&config.Config{}).Execute(context.Background(), codexRealmTestAuth(server.URL), cliproxyexecutor.Request{
		Model: "gpt-6-astra", Payload: codexRealmTestPayload("thread-retry-sync"),
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai-response")})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(string(resp.Payload), "resp_ok") {
		t.Fatalf("client did not receive the retried response: %s", resp.Payload)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("upstream calls = %d, want 2", got)
	}
}

// A request with nothing to strip surfaces the upstream error unchanged
// rather than looping.
func TestCodexAutoExecutorDoesNotRetryWithoutEncryptedContentToStrip(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","code":"invalid_encrypted_content","message":"could not be verified"}}`))
	}))
	defer server.Close()
	_, err := NewCodexAutoExecutor(&config.Config{}).Execute(context.Background(), codexRealmTestAuth(server.URL), cliproxyexecutor.Request{
		Model: "gpt-6-astra", Payload: []byte(`{"model":"gpt-6-astra","input":[{"type":"message","role":"user","content":"hello"}]}`),
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai-response")})
	if err == nil {
		t.Fatal("expected the upstream error to surface")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("upstream calls = %d, want 1", got)
	}
}
