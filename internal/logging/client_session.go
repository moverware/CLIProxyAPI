package logging

// FORK: the client thread a request belongs to, carried on the request
// context so the usage queue can attribute spend per conversation. Claude
// Code sends its session id inside metadata.user_id as a JSON string; Codex
// keys a thread by prompt_cache_key, with turn metadata and the session_id
// header as fallbacks. The value is opaque to the proxy.

import (
	"context"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

type clientSessionKey struct{}

// WithClientSession stores the client's thread identifier in ctx.
func WithClientSession(ctx context.Context, sessionID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, clientSessionKey{}, sessionID)
}

// GetClientSession returns the client's thread identifier stored in ctx.
func GetClientSession(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if value, ok := ctx.Value(clientSessionKey{}).(string); ok {
		return value
	}
	return ""
}

// ClientSessionFromRequest derives the thread identifier from a request body
// and headers. It returns "" when the client sent nothing recognizable.
func ClientSessionFromRequest(body []byte, headers http.Header) string {
	if len(body) > 0 {
		// Claude Code: metadata.user_id = {"device_id":..,"account_uuid":..,"session_id":..}
		if userID := gjson.GetBytes(body, "metadata.user_id").String(); userID != "" {
			if sessionID := strings.TrimSpace(gjson.Get(userID, "session_id").String()); sessionID != "" {
				return sessionID
			}
		}
		// Codex: prompt_cache_key is the thread id for the Responses API.
		if key := strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String()); key != "" {
			return key
		}
		if turn := strings.TrimSpace(gjson.GetBytes(body, "client_metadata.x-codex-turn-metadata").String()); turn != "" {
			if key := strings.TrimSpace(gjson.Get(turn, "prompt_cache_key").String()); key != "" {
				return key
			}
		}
	}
	if headers != nil {
		if turn := strings.TrimSpace(headers.Get("X-Codex-Turn-Metadata")); turn != "" {
			if key := strings.TrimSpace(gjson.Get(turn, "prompt_cache_key").String()); key != "" {
				return key
			}
		}
		for _, name := range []string{"Session_id", "Session-Id", "Conversation_id", "Conversation-Id"} {
			if value := strings.TrimSpace(headers.Get(name)); value != "" {
				return value
			}
		}
	}
	return ""
}
