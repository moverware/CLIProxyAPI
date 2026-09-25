package logging

import (
	"context"
	"net/http"
	"testing"
)

func TestClientSessionFromRequest(t *testing.T) {
	claude := []byte(`{"model":"claude-fable-5-1","metadata":{"user_id":"{\"device_id\":\"d\",\"account_uuid\":\"\",\"session_id\":\"39b5e0b9-320c-42a7-a961-4c79bc0a3d0\"}"}}`)
	if got := ClientSessionFromRequest(claude, nil); got != "39b5e0b9-320c-42a7-a961-4c79bc0a3d0" {
		t.Fatalf("claude session = %q", got)
	}
	codex := []byte(`{"model":"gpt-6-astra","prompt_cache_key":"01a0a1de-3a0f-76b2-9b31-7c444bc864bf","input":[]}`)
	if got := ClientSessionFromRequest(codex, nil); got != "01a0a1de-3a0f-76b2-9b31-7c444bc864bf" {
		t.Fatalf("codex session = %q", got)
	}
	headers := http.Header{}
	headers.Set("Session_id", "hdr-session")
	if got := ClientSessionFromRequest([]byte(`{"model":"x"}`), headers); got != "hdr-session" {
		t.Fatalf("header session = %q", got)
	}
	if got := ClientSessionFromRequest([]byte(`{"model":"x"}`), nil); got != "" {
		t.Fatalf("anonymous request = %q, want empty", got)
	}
}

func TestClientSessionContextRoundTrip(t *testing.T) {
	ctx := WithClientSession(context.Background(), " s1 ")
	if got := GetClientSession(ctx); got != "s1" {
		t.Fatalf("got %q", got)
	}
	if got := GetClientSession(WithClientSession(context.Background(), "")); got != "" {
		t.Fatalf("empty session stored as %q", got)
	}
}
