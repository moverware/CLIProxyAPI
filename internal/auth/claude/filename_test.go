package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCredentialFileNameSeparatesOrganizations(t *testing.T) {
	dir := t.TempDir()
	legacy := "claude-same@example.com.json"
	if err := os.WriteFile(filepath.Join(dir, legacy), []byte(`{"email":"same@example.com","organization_uuid":"personal"}`), 0600); err != nil {
		t.Fatal(err)
	}
	personal, err := CredentialFileName(dir, "same@example.com", "personal")
	if err != nil || personal != legacy {
		t.Fatalf("existing identity: %s %v", personal, err)
	}
	org, err := CredentialFileName(dir, "same@example.com", "company")
	if err != nil || org == personal {
		t.Fatalf("separate identity: %s %v", org, err)
	}
	if _, err = CredentialFileName(dir, "same@example.com", ""); err == nil {
		t.Fatal("missing organization must not overwrite an existing login")
	}
}
