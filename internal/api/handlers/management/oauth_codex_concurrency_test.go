package management

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codex"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

type fakeCodexOAuthService struct {
	bundle *codex.CodexAuthBundle
}

func (f *fakeCodexOAuthService) GenerateAuthURL(state string, pkceCodes *codex.PKCECodes) (string, error) {
	return "https://auth.example.test/oauth?state=" + state, nil
}

func (f *fakeCodexOAuthService) ExchangeCodeForTokens(ctx context.Context, code string, pkceCodes *codex.PKCECodes) (*codex.CodexAuthBundle, error) {
	if f.bundle != nil {
		return f.bundle, nil
	}
	now := time.Now()
	return &codex.CodexAuthBundle{
		TokenData: codex.CodexTokenData{
			IDToken:      "invalid-test-id-token",
			AccessToken:  "access-" + code,
			RefreshToken: "refresh-" + code,
			Email:        "codex-" + code + "@example.test",
			Expire:       now.Add(time.Hour).Format(time.RFC3339),
		},
		LastRefresh: now.Format(time.RFC3339),
	}, nil
}

func TestRequestCodexTokenReusesAccountFileAfterPlanChange(t *testing.T) {
	authDir := t.TempDir()
	personalFile := filepath.Join(authDir, "codex-user@example.com-pro.json")
	teamFile := filepath.Join(authDir, "codex-user@example.com-team.json")
	teamContent := []byte(`{"type":"codex","account_id":"team","email":"user@example.com","access_token":"team-access"}`)
	for path, content := range map[string][]byte{
		personalFile: []byte(`{"type":"codex","account_id":"personal","email":"user@example.com","access_token":"old-access","disabled":true,"weight":7}`),
		teamFile:     teamContent,
	} {
		if errWrite := os.WriteFile(path, content, 0o600); errWrite != nil {
			t.Fatal(errWrite)
		}
	}
	claims := []byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"personal","chatgpt_plan_type":"promax"}}`)
	bundle := &codex.CodexAuthBundle{TokenData: codex.CodexTokenData{
		Email: "user@example.com", AccountID: "personal",
		IDToken:     "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".test",
		AccessToken: "new-access", RefreshToken: "new-refresh",
	}}
	originalNewCodexOAuthService := newCodexOAuthService
	newCodexOAuthService = func(*config.Config) codexOAuthService { return &fakeCodexOAuthService{bundle: bundle} }
	defer func() { newCodexOAuthService = originalNewCodexOAuthService }()
	handler := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, nil)
	router := gin.New()
	router.GET("/codex-auth-url", handler.RequestCodexToken)
	state := requestCodexTokenState(t, router)
	defer CompleteOAuthSession(state)
	if _, errWrite := WriteOAuthCallbackFileForPendingSession(authDir, "codex", state, "upgrade-code", ""); errWrite != nil {
		t.Fatal(errWrite)
	}
	waitForOAuthSessionDone(t, state)
	raw, errRead := os.ReadFile(personalFile)
	if errRead != nil {
		t.Fatal(errRead)
	}
	var saved map[string]any
	if errDecode := json.Unmarshal(raw, &saved); errDecode != nil {
		t.Fatal(errDecode)
	}
	if saved["access_token"] != "new-access" || saved["refresh_token"] != "new-refresh" || saved["disabled"] != true || saved["weight"] != float64(7) {
		t.Fatal("web login did not replace credentials and preserve configured settings")
	}
	teamRaw, errReadTeam := os.ReadFile(teamFile)
	if errReadTeam != nil || string(teamRaw) != string(teamContent) {
		t.Fatal("same-email team credentials changed")
	}
	entries, _ := os.ReadDir(authDir)
	if len(entries) != 2 {
		t.Fatalf("auth directory contains %d files, want 2", len(entries))
	}
}

func (f *fakeCodexOAuthService) CreateTokenStorage(bundle *codex.CodexAuthBundle) *codex.CodexTokenStorage {
	return &codex.CodexTokenStorage{
		IDToken:      bundle.TokenData.IDToken,
		AccessToken:  bundle.TokenData.AccessToken,
		RefreshToken: bundle.TokenData.RefreshToken,
		AccountID:    bundle.TokenData.AccountID,
		LastRefresh:  bundle.LastRefresh,
		Email:        bundle.TokenData.Email,
		Expire:       bundle.TokenData.Expire,
	}
}

func TestRequestCodexTokenCompletionKeepsConcurrentSessionPending(t *testing.T) {
	originalNewCodexOAuthService := newCodexOAuthService
	newCodexOAuthService = func(cfg *config.Config) codexOAuthService {
		return &fakeCodexOAuthService{}
	}
	defer func() {
		newCodexOAuthService = originalNewCodexOAuthService
	}()

	authDir := filepath.Join(t.TempDir(), "auths")
	handler := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, nil)
	router := gin.New()
	router.GET("/codex-auth-url", handler.RequestCodexToken)

	firstState := requestCodexTokenState(t, router)
	secondState := requestCodexTokenState(t, router)
	defer CompleteOAuthSession(firstState)
	defer CompleteOAuthSession(secondState)

	if _, errWrite := WriteOAuthCallbackFileForPendingSession(authDir, "codex", firstState, "first-code", ""); errWrite != nil {
		t.Fatalf("write first callback file: %v", errWrite)
	}

	waitForOAuthSessionDone(t, firstState)
	if !IsOAuthSessionPending(secondState, "codex") {
		t.Fatalf("expected concurrent codex session %s to remain pending after %s completed", secondState, firstState)
	}
}

func requestCodexTokenState(t *testing.T, router http.Handler) string {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/codex-auth-url", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d with body %s", http.StatusOK, w.Code, w.Body.String())
	}

	var payload struct {
		State string `json:"state"`
	}
	if errDecode := json.Unmarshal(w.Body.Bytes(), &payload); errDecode != nil {
		t.Fatalf("decode codex auth URL response: %v", errDecode)
	}
	if payload.State == "" {
		t.Fatalf("expected codex auth URL response to include state")
	}
	return payload.State
}

func waitForOAuthSessionDone(t *testing.T, state string) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !IsOAuthSessionPending(state, "codex") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for codex session %s to complete", state)
}
