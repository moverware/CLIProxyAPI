package auth

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestFileTokenStoreRepairsPermissionsOnUnchangedSave(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir := t.TempDir()
	store := NewFileTokenStore()
	store.SetBaseDir(dir)
	auth := &cliproxyauth.Auth{ID: "test.json", Provider: "test", Metadata: map[string]any{"type": "test", "access_token": "synthetic"}}
	if _, err := store.Save(context.Background(), auth); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "test.json")
	for target, mode := range map[string]os.FileMode{dir: 0o755, path: 0o644} {
		if err := os.Chmod(target, mode); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Save(context.Background(), auth); err != nil {
		t.Fatal(err)
	}
	for target, want := range map[string]os.FileMode{dir: 0o700, path: 0o600} {
		info, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Fatalf("mode = %o, want %o", info.Mode().Perm(), want)
		}
	}
}
