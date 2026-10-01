package codex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// CredentialFileNameForAccount keeps a login's file identity through changes to
// its subscription plan or email. Distinct personal and team accounts retain
// separate files even when they use the same email.
func CredentialFileNameForAccount(authDir, email, planType, accountID string, includeProviderPrefix bool) (string, error) {
	accountID = strings.TrimSpace(accountID)
	hashAccountID := ""
	if accountID != "" {
		digest := sha256.Sum256([]byte(accountID))
		hashAccountID = hex.EncodeToString(digest[:])[:8]
	}
	fileName := CredentialFileName(email, planType, hashAccountID, includeProviderPrefix)
	if accountID == "" || strings.TrimSpace(authDir) == "" {
		return fileName, nil
	}
	entries, errReadDir := os.ReadDir(authDir)
	if os.IsNotExist(errReadDir) {
		return fileName, nil
	}
	if errReadDir != nil {
		return "", fmt.Errorf("read Codex auth directory: %w", errReadDir)
	}
	existingName := ""
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, errRead := os.ReadFile(filepath.Join(authDir, entry.Name()))
		if errRead != nil {
			continue
		}
		var stored struct {
			Type      string `json:"type"`
			AccountID string `json:"account_id"`
		}
		if json.Unmarshal(raw, &stored) != nil || stored.Type != "codex" || strings.TrimSpace(stored.AccountID) != accountID {
			continue
		}
		if entry.Name() == fileName {
			return fileName, nil
		}
		if existingName == "" {
			existingName = entry.Name()
		}
	}
	if existingName != "" {
		return existingName, nil
	}
	return fileName, nil
}

// CredentialFileName returns the filename used to persist Codex OAuth credentials.
// The account hash is included when available to keep accounts with the same email
// and plan distinct. The legacy email-based format remains the fallback.
func CredentialFileName(email, planType, hashAccountID string, includeProviderPrefix bool) string {
	email = strings.TrimSpace(email)
	plan := normalizePlanTypeForFilename(planType)
	hashAccountID = strings.TrimSpace(hashAccountID)

	prefix := ""
	if includeProviderPrefix {
		prefix = "codex"
	}

	if hashAccountID != "" {
		if plan == "" {
			return fmt.Sprintf("%s-%s-%s.json", prefix, hashAccountID, email)
		}
		return fmt.Sprintf("%s-%s-%s-%s.json", prefix, hashAccountID, email, plan)
	}
	if plan == "" {
		return fmt.Sprintf("%s-%s.json", prefix, email)
	}
	return fmt.Sprintf("%s-%s-%s.json", prefix, email, plan)
}

func normalizePlanTypeForFilename(planType string) string {
	planType = strings.TrimSpace(planType)
	if planType == "" {
		return ""
	}

	parts := strings.FieldsFunc(planType, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(parts) == 0 {
		return ""
	}

	for i, part := range parts {
		parts[i] = strings.ToLower(strings.TrimSpace(part))
	}
	return strings.Join(parts, "-")
}
