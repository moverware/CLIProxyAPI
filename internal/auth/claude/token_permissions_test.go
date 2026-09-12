package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCredentialFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir := filepath.Join(t.TempDir(), "credentials")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "auth.json")
	storage := &ClaudeTokenStorage{AccessToken: "synthetic-token", RefreshToken: "synthetic-refresh"}
	for _, existing := range []bool{false, true} {
		if existing {
			if err := os.WriteFile(path, []byte(`{"access_token":"old-long-synthetic-token-to-detect-trailing-data"}`), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := storage.SaveTokenToFile(path); err != nil {
			t.Fatal(err)
		}
		for target, want := range map[string]os.FileMode{path: 0o600, dir: 0o700} {
			info, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != want {
				t.Fatalf("mode = %o, want %o", info.Mode().Perm(), want)
			}
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var data map[string]any
		if err := json.Unmarshal(raw, &data); err != nil {
			t.Fatal(err)
		}
		if data["access_token"] != "synthetic-token" {
			t.Fatal("credential contents changed")
		}
	}
}
