package executor

// FORK: encryption realms for Codex credential failover. Reasoning items and
// agent-message parts carry `encrypted_content` that only the upstream which
// produced them can decrypt: the ChatGPT backend behind subscription logins
// cannot read what an Azure OpenAI resource encrypted, and vice versa. A
// thread that fails over between them (an exhausted subscription, a dead
// refresh token, the last-resort key) therefore replays ciphertext its new
// upstream rejects with `invalid_encrypted_content`, and every later turn of
// that thread fails the same way until someone hand-edits the client's
// transcript.
//
// Two layers keep a failover to a single turn of lost reasoning continuity:
//
//  1. Proactive: each session remembers the realm that last completed a
//     response. A request landing on a different realm has its encrypted
//     items stripped before it leaves, and the session's reasoning replay
//     cache (also realm-bound ciphertext) is dropped.
//  2. Reactive: when the upstream still answers `invalid_encrypted_content`
//     (no realm history after a restart, a new item shape), the request is
//     stripped of every encrypted item and sent once more before the client
//     sees an error.
//
// A realm is the upstream host. Subscription logins on the same backend share
// one realm, so ordinary account failover inside the pool costs nothing.

import (
	"context"
	"net/url"
	"strings"

	internalcache "github.com/router-for-me/CLIProxyAPI/v7/internal/cache"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const codexEncryptionRealmDefault = "chatgpt.com"

// codexEncryptionRealms maps a session key to the realm that last completed a
// response for it. Sessions that vanish from the window simply lose the
// proactive layer and fall back to the reactive retry.
var codexEncryptionRealms = internalcache.NewBoundedLRU[string, *string](8192, nil)

type codexEncryptionRealmScope struct {
	sessionKey string
	realm      string
	modelName  string
}

func (s codexEncryptionRealmScope) valid() bool {
	return s.sessionKey != "" && s.realm != ""
}

// codexEncryptionRealm names the upstream family that can decrypt content
// produced through auth: the base URL host for a codex-api-key entry, the
// ChatGPT backend for subscription logins.
func codexEncryptionRealm(auth *cliproxyauth.Auth) string {
	_, baseURL := codexCreds(auth)
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return codexEncryptionRealmDefault
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" {
		return strings.ToLower(baseURL)
	}
	return strings.ToLower(parsed.Host)
}

// codexEncryptionSessionKey identifies the client thread. It deliberately
// omits the replay cache's proxy-API-key fallback: every pool client shares
// one key, and treating them as a single thread would strip content on every
// credential change between unrelated sessions.
func codexEncryptionSessionKey(ctx context.Context, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, body []byte) string {
	if value := metadataString(opts.Metadata, cliproxyexecutor.ExecutionSessionMetadataKey); value != "" {
		return "execution:" + value
	}
	if value := metadataString(req.Metadata, cliproxyexecutor.ExecutionSessionMetadataKey); value != "" {
		return "execution:" + value
	}
	if value := codexReasoningReplaySessionKeyFromPayload(body); value != "" {
		return value
	}
	if value := codexReasoningReplaySessionKeyFromPayload(req.Payload); value != "" {
		return value
	}
	return codexReasoningReplaySessionKeyFromHeaders(opts.Headers)
}

func newCodexEncryptionRealmScope(ctx context.Context, from sdktranslator.Format, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, body []byte) codexEncryptionRealmScope {
	modelName := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	return codexEncryptionRealmScope{
		sessionKey: codexEncryptionSessionKey(ctx, req, opts, body),
		realm:      codexEncryptionRealm(auth),
		modelName:  modelName,
	}
}

// prepare strips realm-bound content when the session last completed on a
// different realm, and drops that session's reasoning replay cache.
func (s codexEncryptionRealmScope) prepare(ctx context.Context, body []byte) []byte {
	if !s.valid() {
		return body
	}
	previous, ok := codexEncryptionRealms.Get(s.sessionKey)
	if !ok || previous == nil || *previous == s.realm {
		return body
	}
	updated, stripped := stripCodexEncryptedContent(body)
	if stripped > 0 {
		helps.LogWithRequestID(ctx).Infof("codex executor: session %s moved from %s to %s, stripped %d encrypted item(s) the new upstream cannot decrypt", s.sessionKey, *previous, s.realm, stripped)
	}
	if s.modelName != "" {
		internalcache.DeleteCodexReasoningReplayItem(s.modelName, s.sessionKey)
	}
	return updated
}

// record remembers that this session completed a response on this realm.
func (s codexEncryptionRealmScope) record() {
	if !s.valid() {
		return
	}
	realm := s.realm
	slot := codexEncryptionRealms.GetOrAdd(s.sessionKey, func() *string { return new(string) })
	*slot = realm
}

// stripCodexEncryptedContent removes every provider-encrypted payload from a
// Responses request: reasoning items lose encrypted_content (and their id,
// which would otherwise read as a store lookup), and content parts of type
// encrypted_content disappear from message items. It returns the number of
// items changed.
func stripCodexEncryptedContent(body []byte) ([]byte, int) {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, 0
	}
	items := input.Array()
	rebuilt := make([]string, 0, len(items))
	stripped := 0
	for _, item := range items {
		raw := item.Raw
		changed := false
		if strings.TrimSpace(item.Get("type").String()) == "reasoning" && item.Get("encrypted_content").Exists() {
			raw, _ = sjson.Delete(raw, "encrypted_content")
			if !gjson.GetBytes(body, "store").Bool() {
				raw, _ = sjson.Delete(raw, "id")
			}
			changed = true
		}
		if content := item.Get("content"); content.IsArray() {
			parts := content.Array()
			kept := make([]string, 0, len(parts))
			for _, part := range parts {
				if strings.TrimSpace(part.Get("type").String()) == "encrypted_content" || part.Get("encrypted_content").Exists() {
					changed = true
					continue
				}
				kept = append(kept, part.Raw)
			}
			if len(kept) != len(parts) {
				if len(kept) == 0 {
					stripped++
					continue
				}
				raw, _ = sjson.SetRaw(raw, "content", "["+strings.Join(kept, ",")+"]")
			}
		}
		if changed {
			stripped++
		}
		rebuilt = append(rebuilt, raw)
	}
	if stripped == 0 {
		return body, 0
	}
	updated, err := sjson.SetRawBytes(body, "input", []byte("["+strings.Join(rebuilt, ",")+"]"))
	if err != nil {
		return body, 0
	}
	return updated, stripped
}

// isCodexEncryptedContentRejection reports whether err is the upstream's
// refusal to decrypt replayed content.
func isCodexEncryptedContentRejection(err error) bool {
	if err == nil {
		return false
	}
	var status int
	if coder, ok := err.(interface{ StatusCode() int }); ok {
		status = coder.StatusCode()
	}
	body := []byte(err.Error())
	if gjson.GetBytes(body, "error.code").String() == "thinking_signature_invalid" {
		return true
	}
	code, _, ok := codexStatusErrorClassification(status, body)
	return ok && code == "thinking_signature_invalid"
}

// retryWithoutEncryptedContent re-issues a request once with every encrypted
// item removed after the upstream rejected the ciphertext. Only payloads that
// carry a Responses input array can be stripped; anything else is returned
// unchanged so the caller surfaces the original error.
func retryWithoutEncryptedContent(ctx context.Context, req cliproxyexecutor.Request) (cliproxyexecutor.Request, bool) {
	stripped, count := stripCodexEncryptedContent(req.Payload)
	if count == 0 {
		return req, false
	}
	helps.LogWithRequestID(ctx).Infof("codex executor: upstream rejected replayed encrypted content, retrying once with %d encrypted item(s) removed", count)
	req.Payload = stripped
	return req, true
}

// retryExecuteWithoutEncryptedContent runs one attempt and, on an encrypted
// content rejection, one stripped retry.
func retryExecuteWithoutEncryptedContent(ctx context.Context, req cliproxyexecutor.Request, run func(cliproxyexecutor.Request) (cliproxyexecutor.Response, error)) (cliproxyexecutor.Response, error) {
	resp, err := run(req)
	if !isCodexEncryptedContentRejection(err) {
		return resp, err
	}
	stripped, ok := retryWithoutEncryptedContent(ctx, req)
	if !ok {
		return resp, err
	}
	return run(stripped)
}

// retryExecuteStreamWithoutEncryptedContent is the streaming counterpart. A
// rejection that arrives before the stream commits (an HTTP error, or a
// terminal event held back by bootstrap buffering) surfaces as the returned
// error and is retried; one delivered inside an already committed stream has
// reached the client and cannot be.
func retryExecuteStreamWithoutEncryptedContent(ctx context.Context, req cliproxyexecutor.Request, run func(cliproxyexecutor.Request) (*cliproxyexecutor.StreamResult, error)) (*cliproxyexecutor.StreamResult, error) {
	result, err := run(req)
	if !isCodexEncryptedContentRejection(err) {
		return result, err
	}
	stripped, ok := retryWithoutEncryptedContent(ctx, req)
	if !ok {
		return result, err
	}
	return run(stripped)
}
