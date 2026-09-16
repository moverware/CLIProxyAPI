package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestClaudeExecutorPreservesSystemOrderAfterThinkingRemoval(t *testing.T) {
	for _, mode := range []string{"execute", "stream", "count_tokens"} {
		t.Run(mode, func(t *testing.T) {
			seen := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				seen <- body
				messages := gjson.GetBytes(body, "messages").Array()
				for i, message := range messages {
					if message.Get("role").String() == "system" && i+1 < len(messages) && messages[i+1].Get("role").String() != "assistant" {
						http.Error(w, `{"error":{"type":"invalid_request_error","message":"system must precede assistant or end array"}}`, http.StatusBadRequest)
						return
					}
				}
				if mode == "stream" {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"type\":\"message_stop\"}\n\n")
				} else if mode == "count_tokens" {
					_, _ = io.WriteString(w, `{"input_tokens":10}`)
				} else {
					_, _ = io.WriteString(w, `{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":10,"output_tokens":1}}`)
				}
			}))
			defer server.Close()
			executor := NewClaudeExecutor(&config.Config{})
			auth := &cliproxyauth.Auth{Attributes: map[string]string{"api_key": "test-key", "base_url": server.URL}}
			payload := []byte(`{"model":"claude-fable-5-1","max_tokens":16,"messages":[
				{"role":"user","content":"Read the board"},
				{"role":"system","content":[{"type":"text","text":"Only you see that command output."}]},
				{"role":"assistant","content":[{"type":"thinking","thinking":"","signature":""}]},
				{"role":"user","content":"The user named this session auto review."},
				{"role":"assistant","content":[{"type":"tool_use","id":"toolu_test","name":"Bash","input":{"command":"pwd"}}]},
				{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_test","content":"/tmp"}]}
			],"tools":[{"name":"Bash","input_schema":{"type":"object"}}]}`)
			req := cliproxyexecutor.Request{Model: "claude-fable-5-1", Payload: payload}
			opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")}
			var err error
			switch mode {
			case "execute":
				_, err = executor.Execute(context.Background(), auth, req, opts)
			case "stream":
				var result *cliproxyexecutor.StreamResult
				result, err = executor.ExecuteStream(context.Background(), auth, req, opts)
				if err == nil {
					for chunk := range result.Chunks {
						if chunk.Err != nil {
							t.Fatal(chunk.Err)
						}
					}
				}
			case "count_tokens":
				// Custom base URLs select a local estimator; exercise the native
				// HTTP preparation directly against this test server.
				_, err = executor.countTokensUpstream(context.Background(), auth, req, opts)
			}
			if err != nil {
				t.Fatal(err)
			}
			body := <-seen
			if gjson.GetBytes(body, "messages.1.role").String() != "system" || gjson.GetBytes(body, "messages.1.content.0.text").String() != "Only you see that command output." {
				t.Fatalf("system instruction changed: %s", body)
			}
			if gjson.GetBytes(body, "messages.2.content.0.text").String() != "[Thinking omitted]" {
				t.Fatalf("missing assistant boundary: %s", body)
			}
			if gjson.GetBytes(body, "messages.4.content.0.id").String() != "toolu_test" || gjson.GetBytes(body, "messages.5.content.0.tool_use_id").String() != "toolu_test" {
				t.Fatalf("tool call/result adjacency changed: %s", body)
			}
		})
	}
}
