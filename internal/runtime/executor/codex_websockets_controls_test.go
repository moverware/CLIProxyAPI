package executor

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func TestCodexWebsocketControlUsesExistingConnectionUnchanged(t *testing.T) {
	received := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, payload, err := conn.ReadMessage()
		if err == nil {
			received <- payload
		}
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	exec := &CodexWebsocketsExecutor{store: &codexWebsocketSessionStore{sessions: map[string]*codexWebsocketSession{"active": {conn: conn}}}}
	payload := []byte(`{"type":"response.interrupt","response_id":"resp-active","mode":"discard_partial_items"}`)
	if err := exec.SendExecutionSessionControl("active", payload); err != nil {
		t.Fatal(err)
	}
	select {
	case actual := <-received:
		if !bytes.Equal(actual, payload) {
			t.Fatalf("actual=%s want=%s", actual, payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("control never reached existing upstream")
	}
	if len(exec.store.sessions) != 1 {
		t.Fatalf("control created a session")
	}
}

func TestCodexWebsocketControlDoesNotCreateMissingUpstream(t *testing.T) {
	exec := &CodexWebsocketsExecutor{store: &codexWebsocketSessionStore{sessions: map[string]*codexWebsocketSession{"idle": {}}}}
	for _, id := range []string{"missing", "idle"} {
		if err := exec.SendExecutionSessionControl(id, []byte(`{"type":"response.interrupt"}`)); !cliproxyexecutor.IsUpstreamWebsocketReplayRequired(err) {
			t.Fatalf("session=%s err=%v", id, err)
		}
	}
	if len(exec.store.sessions) != 1 {
		t.Fatal("control created a missing session")
	}
}
