package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codex"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestCodexReloginReusesAccountFileAndSettings(t *testing.T) {
	authDir := t.TempDir()
	personalName := "codex-user@example.com-pro.json"
	teamName := "codex-user@example.com-team.json"
	for name, content := range map[string]string{
		personalName: `{"type":"codex","account_id":"personal","email":"user@example.com","access_token":"old","disabled":true,"weight":7}`,
		teamName:     `{"type":"codex","account_id":"team","email":"user@example.com","access_token":"team-old","weight":3}`,
	} {
		if errWrite := os.WriteFile(filepath.Join(authDir, name), []byte(content), 0o600); errWrite != nil {
			t.Fatal(errWrite)
		}
	}
	cfg := &config.Config{AuthDir: authDir}
	for _, tt := range []struct {
		accountID, plan, fileName string
		disabled                  bool
		weight                    float64
	}{
		{"personal", "promax", personalName, true, 7},
		{"team", "team", teamName, false, 3},
	} {
		t.Run(tt.accountID, func(t *testing.T) {
			claims, _ := json.Marshal(map[string]any{"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": tt.accountID, "chatgpt_plan_type": tt.plan}})
			idToken := "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".test"
			bundle := &codex.CodexAuthBundle{TokenData: codex.CodexTokenData{Email: "user@example.com", AccountID: tt.accountID, IDToken: idToken, AccessToken: "new-access", RefreshToken: "new-refresh"}}
			record, errBuild := NewCodexAuthenticator().buildAuthRecord(cfg, codex.NewCodexAuth(cfg), bundle)
			if errBuild != nil {
				t.Fatal(errBuild)
			}
			manager := NewManager(NewFileTokenStore(), &dummyAuthenticator{provider: "codex", record: record})
			_, savedPath, errLogin := manager.Login(context.Background(), "codex", cfg, nil)
			if errLogin != nil {
				t.Fatal(errLogin)
			}
			if record.ID != tt.fileName || savedPath != filepath.Join(authDir, tt.fileName) {
				t.Fatalf("login identity = %q, saved path = %q", record.ID, savedPath)
			}
			raw, errRead := os.ReadFile(savedPath)
			if errRead != nil {
				t.Fatal(errRead)
			}
			var saved map[string]any
			if errDecode := json.Unmarshal(raw, &saved); errDecode != nil {
				t.Fatal(errDecode)
			}
			if saved["access_token"] != "new-access" || saved["refresh_token"] != "new-refresh" || saved["disabled"] != tt.disabled || saved["weight"] != tt.weight {
				t.Fatal("login did not replace credentials and preserve configured settings")
			}
		})
	}
	entries, _ := os.ReadDir(authDir)
	if len(entries) != 2 {
		t.Fatalf("auth directory contains %d files, want 2", len(entries))
	}
}
