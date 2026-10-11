package openai

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
	"github.com/tidwall/gjson"
)

type interruptWebsocketExecutor struct {
	websocketCaptureExecutor
	control   chan []byte
	requests  atomic.Int32
	sessionID chan string
}

func (*interruptWebsocketExecutor) Identifier() string { return "codex" }

func (e *interruptWebsocketExecutor) ExecuteStream(ctx context.Context, _ *coreauth.Auth, _ coreexecutor.Request, opts coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	call := e.requests.Add(1)
	chunks := make(chan coreexecutor.StreamChunk, 1)
	go func() {
		defer close(chunks)
		if call == 1 {
			e.sessionID <- opts.Metadata[coreexecutor.ExecutionSessionMetadataKey].(string)
			chunks <- coreexecutor.StreamChunk{Payload: []byte(`{"type":"response.created","response":{"id":"resp-active"}}`)}
			select {
			case <-e.control:
			case <-ctx.Done():
				return
			}
		}
		chunks <- coreexecutor.StreamChunk{Payload: []byte(`{"type":"response.completed","response":{"id":"resp-finished","output":[]}}`)}
	}()
	return &coreexecutor.StreamResult{Chunks: chunks}, nil
}

func (e *interruptWebsocketExecutor) SendExecutionSessionControl(sessionID string, payload []byte) error {
	e.sessionID <- sessionID
	e.control <- bytes.Clone(payload)
	return nil
}

func TestResponsesWebsocketInterruptDuringActiveResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	exec := &interruptWebsocketExecutor{control: make(chan []byte, 1), sessionID: make(chan string, 2)}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(exec)
	auth := &coreauth.Auth{ID: "interrupt-auth", Provider: "codex", Status: coreauth.StatusActive, Attributes: map[string]string{"websockets": "true"}}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatal(err)
	}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: "interrupt-model"}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
	h := NewOpenAIResponsesAPIHandler(handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager))
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)
	server := httptest.NewServer(router)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"interrupt-model","input":[]}`)); err != nil {
		t.Fatal(err)
	}
	_, created, err := conn.ReadMessage()
	if err != nil || gjson.GetBytes(created, "type").String() != "response.created" {
		t.Fatalf("created=%s err=%v", created, err)
	}
	// Native Codex sends this after receiving response.created, before the stream ends.
	control := []byte(`{"type":"response.interrupt","response_id":"resp-active","mode":"discard_partial_items"}`)
	if err = conn.WriteMessage(websocket.TextMessage, control); err != nil {
		t.Fatal(err)
	}
	_, completed, err := conn.ReadMessage()
	if err != nil || gjson.GetBytes(completed, "type").String() != "response.completed" {
		t.Fatalf("completed=%s err=%v", completed, err)
	}
	session := <-exec.sessionID
	if actual := <-exec.sessionID; actual == "" || actual != session {
		t.Fatalf("control session=%q want=%q", actual, session)
	}
	if exec.requests.Load() != 1 {
		t.Fatalf("interrupt created another inference: %d", exec.requests.Load())
	}
	// The same connection remains usable for ordinary continuation.
	if err = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","input":[]}`)); err != nil {
		t.Fatal(err)
	}
	_, completed, err = conn.ReadMessage()
	if err != nil || gjson.GetBytes(completed, "type").String() != "response.completed" {
		t.Fatalf("continuation=%s err=%v", completed, err)
	}
	if exec.requests.Load() != 2 {
		t.Fatalf("requests=%d want=2", exec.requests.Load())
	}
}

func TestResponsesWebsocketInterruptWithoutUpstreamRequestsReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewOpenAIResponsesAPIHandler(handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, coreauth.NewManager(nil, nil, nil)))
	router := gin.New()
	router.GET("/v1/responses", h.ResponsesWebsocket)
	server := httptest.NewServer(router)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.interrupt","response_id":"missing","mode":"discard_partial_items"}`)); err != nil {
		t.Fatal(err)
	}
	_, payload, err := conn.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseServiceRestart) {
		t.Fatalf("payload=%s err=%v want replay close", payload, err)
	}
}
