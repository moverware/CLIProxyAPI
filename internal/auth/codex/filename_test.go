package codex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCredentialFileNameForAccount(t *testing.T) {
	authDir := t.TempDir()
	personalFile := "codex-user@example.com-pro.json"
	teamFile := "codex-user@example.com-team.json"
	for name, content := range map[string]string{
		personalFile:  `{"type":"codex","account_id":"personal","email":"user@example.com"}`,
		teamFile:      `{"type":"codex","account_id":"team","email":"user@example.com"}`,
		"broken.json": `{`,
	} {
		if errWrite := os.WriteFile(filepath.Join(authDir, name), []byte(content), 0o600); errWrite != nil {
			t.Fatal(errWrite)
		}
	}
	for _, tt := range []struct {
		name, email, plan, accountID, want string
	}{
		{"plan upgrade", "user@example.com", "promax", "personal", personalFile},
		{"email change", "new@example.com", "promax", "personal", personalFile},
		{"same-email team", "user@example.com", "team", "team", teamFile},
		{"new account", "user@example.com", "team", "new-team", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, errName := CredentialFileNameForAccount(authDir, tt.email, tt.plan, tt.accountID, true)
			if errName != nil {
				t.Fatal(errName)
			}
			if tt.want == "" {
				if got == personalFile || got == teamFile {
					t.Fatalf("distinct account reused %q", got)
				}
			} else if got != tt.want {
				t.Fatalf("filename = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCredentialFileName(t *testing.T) {
	tests := []struct {
		name                  string
		email                 string
		planType              string
		hashAccountID         string
		includeProviderPrefix bool
		want                  string
	}{
		{
			name:                  "team includes account hash",
			email:                 "user@example.com",
			planType:              "team",
			hashAccountID:         "abc12345",
			includeProviderPrefix: true,
			want:                  "codex-abc12345-user@example.com-team.json",
		},
		{
			name:                  "k12 includes account hash",
			email:                 "user@example.com",
			planType:              "k12",
			hashAccountID:         "def67890",
			includeProviderPrefix: true,
			want:                  "codex-def67890-user@example.com-k12.json",
		},
		{
			name:                  "k12 without account hash falls back to email and plan",
			email:                 "user@example.com",
			planType:              "k12",
			hashAccountID:         "",
			includeProviderPrefix: true,
			want:                  "codex-user@example.com-k12.json",
		},
		{
			name:                  "plus includes account hash",
			email:                 " user@example.com ",
			planType:              "Plus",
			hashAccountID:         " abc12345 ",
			includeProviderPrefix: true,
			want:                  "codex-abc12345-user@example.com-plus.json",
		},
		{
			name:                  "plus without account hash falls back to email and plan",
			email:                 "user@example.com",
			planType:              "plus",
			hashAccountID:         "",
			includeProviderPrefix: true,
			want:                  "codex-user@example.com-plus.json",
		},
		{
			name:                  "plan is normalized",
			email:                 "user@example.com",
			planType:              " Team Plan ",
			hashAccountID:         "abc12345",
			includeProviderPrefix: true,
			want:                  "codex-abc12345-user@example.com-team-plan.json",
		},
		{
			name:                  "account hash is used without plan",
			email:                 "user@example.com",
			planType:              "",
			hashAccountID:         "abc12345",
			includeProviderPrefix: true,
			want:                  "codex-abc12345-user@example.com.json",
		},
		{
			name:                  "missing plan and account hash falls back to email",
			email:                 "user@example.com",
			planType:              "",
			hashAccountID:         "",
			includeProviderPrefix: true,
			want:                  "codex-user@example.com.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CredentialFileName(tt.email, tt.planType, tt.hashAccountID, tt.includeProviderPrefix)
			if got != tt.want {
				t.Fatalf("CredentialFileName() = %q, want %q", got, tt.want)
			}
		})
	}
}
