package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyExtraModelsMergesUnknownIDsOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models-extra.json")
	overlay := `{
		"claude": [
			{"id": "claude-fable-5", "object": "model", "type": "claude", "display_name": "duplicate, must be ignored"},
			{"id": "claude-fable-5-1", "object": "model", "type": "claude", "display_name": "Claude Fable 5.1"}
		],
		"kimi": [
			{"id": "kimi-k9", "object": "model", "type": "kimi"}
		]
	}`
	if err := os.WriteFile(path, []byte(overlay), 0o600); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
	t.Setenv(extraModelsEnv, path)

	parsed := &staticModelsJSON{
		Claude: []*ModelInfo{{ID: "claude-fable-5", Object: "model", Type: "claude", DisplayName: "Claude Fable 5"}},
	}
	applyExtraModels(parsed)

	if len(parsed.Claude) != 2 {
		t.Fatalf("expected 2 claude models after merge, got %d", len(parsed.Claude))
	}
	if parsed.Claude[0].DisplayName != "Claude Fable 5" {
		t.Fatalf("base catalog entry must win for known ids, got %q", parsed.Claude[0].DisplayName)
	}
	if parsed.Claude[1].ID != "claude-fable-5-1" {
		t.Fatalf("expected claude-fable-5-1 appended, got %q", parsed.Claude[1].ID)
	}
	if len(parsed.Kimi) != 1 || parsed.Kimi[0].ID != "kimi-k9" {
		t.Fatalf("expected kimi section merged, got %+v", parsed.Kimi)
	}
}

func TestApplyExtraModelsMissingFileAndEnvAreNoOps(t *testing.T) {
	parsed := &staticModelsJSON{
		Claude: []*ModelInfo{{ID: "claude-fable-5", Object: "model", Type: "claude"}},
	}

	t.Setenv(extraModelsEnv, "")
	applyExtraModels(parsed)
	if len(parsed.Claude) != 1 {
		t.Fatalf("unset env must be a no-op, got %d claude models", len(parsed.Claude))
	}

	t.Setenv(extraModelsEnv, filepath.Join(t.TempDir(), "does-not-exist.json"))
	applyExtraModels(parsed)
	if len(parsed.Claude) != 1 {
		t.Fatalf("missing file must be a no-op, got %d claude models", len(parsed.Claude))
	}
}

func TestApplyExtraModelsMalformedOverlayIsSkipped(t *testing.T) {
	dir := t.TempDir()

	badJSON := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(badJSON, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
	invalid := filepath.Join(dir, "invalid.json")
	// Duplicate ids within a section fail catalog validation.
	if err := os.WriteFile(invalid, []byte(`{"claude": [{"id": "x"}, {"id": "x"}]}`), 0o600); err != nil {
		t.Fatalf("write overlay: %v", err)
	}

	for _, path := range []string{badJSON, invalid} {
		parsed := &staticModelsJSON{
			Claude: []*ModelInfo{{ID: "claude-fable-5", Object: "model", Type: "claude"}},
		}
		t.Setenv(extraModelsEnv, path)
		applyExtraModels(parsed)
		if len(parsed.Claude) != 1 {
			t.Fatalf("overlay %s must be skipped, got %d claude models", path, len(parsed.Claude))
		}
	}
}

func TestMergeModelSectionSkipsNilAndEmptyIDs(t *testing.T) {
	base := []*ModelInfo{{ID: "a"}}
	added := mergeModelSection(&base, []*ModelInfo{nil, {ID: "  "}, {ID: "a"}, {ID: "b"}, {ID: "b"}})
	if added != 1 {
		t.Fatalf("expected 1 added, got %d", added)
	}
	if len(base) != 2 || base[1].ID != "b" {
		t.Fatalf("unexpected merge result: %+v", base)
	}
}
