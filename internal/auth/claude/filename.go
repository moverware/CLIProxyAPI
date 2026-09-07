package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CredentialFileName preserves a login's existing file and separates organizations
// that share an email address. An email alone cannot identify a subscription.
func CredentialFileName(authDir, email, organization string) (string, error) {
	if email == "" || organization == "" || strings.ContainsAny(email+organization, `/\\`) {
		return "", fmt.Errorf("Claude login requires an email and organization identity")
	}
	entries, err := os.ReadDir(authDir)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read Claude auth directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "claude-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, errRead := os.ReadFile(filepath.Join(authDir, entry.Name()))
		if errRead != nil {
			continue
		}
		var stored struct {
			Email        string `json:"email"`
			Organization string `json:"organization_uuid"`
		}
		if json.Unmarshal(data, &stored) == nil && stored.Email == email && stored.Organization == organization {
			return entry.Name(), nil
		}
	}
	return fmt.Sprintf("claude-%s-%s.json", email, organization), nil
}
