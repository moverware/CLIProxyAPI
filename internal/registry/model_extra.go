package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// extraModelsEnv names an optional local catalog overlay file with the same
// JSON shape as models.json. The overlay is merged on top of the embedded or
// remote catalog at startup and on every refresh: entries whose id already
// exists in the base catalog are ignored, so the file can safely carry a full
// provider listing generated from that provider's own /v1/models endpoint
// while the curated catalog remains authoritative for models it knows.
const extraModelsEnv = "CLIPROXY_EXTRA_MODELS_FILE"

// extraModelsPollInterval is how often the overlay file's mtime is checked.
// The overlay's writer picks up a launch within minutes; the base catalog
// refresh runs every few hours, too slow to wait on for a launch-day model.
const extraModelsPollInterval = time.Minute

var extraModelsWatchOnce sync.Once

// applyExtraModels merges the overlay file (if configured and present) into
// the parsed catalog in place. A missing file is not an error — the overlay
// is optional; a malformed file is logged and skipped so a bad overlay can
// never take down the catalog.
func applyExtraModels(parsed *staticModelsJSON) {
	path := strings.TrimSpace(os.Getenv(extraModelsEnv))
	if path == "" || parsed == nil {
		return
	}
	extra, err := loadExtraModels(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Warnf("registry: extra models overlay %s skipped: %v", path, err)
		}
		return
	}
	added := 0
	for _, section := range []struct {
		base  *[]*ModelInfo
		extra []*ModelInfo
	}{
		{&parsed.Claude, extra.Claude},
		{&parsed.Gemini, extra.Gemini},
		{&parsed.Vertex, extra.Vertex},
		{&parsed.AIStudio, extra.AIStudio},
		{&parsed.CodexFree, extra.CodexFree},
		{&parsed.CodexTeam, extra.CodexTeam},
		{&parsed.CodexPlus, extra.CodexPlus},
		{&parsed.CodexPro, extra.CodexPro},
		{&parsed.Kimi, extra.Kimi},
		{&parsed.Antigravity, extra.Antigravity},
		{&parsed.XAI, extra.XAI},
	} {
		added += mergeModelSection(section.base, section.extra)
	}
	if added > 0 {
		log.Infof("registry: extra models overlay %s added %d model(s)", path, added)
	}
}

// startExtraModelsWatcher re-merges the overlay into the published catalog
// whenever the file changes, for the lifetime of ctx.
func startExtraModelsWatcher(ctx context.Context) {
	path := strings.TrimSpace(os.Getenv(extraModelsEnv))
	if path == "" {
		return
	}
	extraModelsWatchOnce.Do(func() {
		go watchExtraModels(ctx, path, extraModelsPollInterval)
	})
}

func watchExtraModels(ctx context.Context, path string, interval time.Duration) {
	last := extraModelsModTime(path)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			modTime := extraModelsModTime(path)
			if modTime.Equal(last) {
				continue
			}
			last = modTime
			notifyModelRefresh(reapplyExtraModels())
		}
	}
}

func extraModelsModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// reapplyExtraModels merges the overlay into the current catalog without
// refetching the base catalog and returns the providers whose models changed.
// Overlay entries only ever add ids, so a model dropped from the overlay stays
// until the next full catalog refresh. The general updater's lock keeps this
// from interleaving with a refresh's publish.
func reapplyExtraModels() []string {
	generalCatalogUpdater.mu.Lock()
	defer generalCatalogUpdater.mu.Unlock()
	current := getModels()
	if current == nil {
		return nil
	}
	next := *current
	applyExtraModels(&next)
	changed := detectChangedProviders(current, &next)
	if len(changed) == 0 {
		return nil
	}
	modelsCatalogStore.mu.Lock()
	modelsCatalogStore.data = &next
	modelsCatalogStore.mu.Unlock()
	return changed
}

func loadExtraModels(path string) (*staticModelsJSON, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var parsed staticModelsJSON
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("decode extra models overlay: %w", err)
	}
	if err := validateModelsCatalog(&parsed); err != nil {
		return nil, fmt.Errorf("validate extra models overlay: %w", err)
	}
	return &parsed, nil
}

// mergeModelSection appends overlay entries whose id is not already present
// in the base section and reports how many were added.
func mergeModelSection(base *[]*ModelInfo, extra []*ModelInfo) int {
	if len(extra) == 0 {
		return 0
	}
	// Appends must not write into a backing array that a published catalog
	// snapshot still shares.
	*base = slices.Clip(*base)
	seen := make(map[string]struct{}, len(*base))
	for _, model := range *base {
		if model == nil {
			continue
		}
		seen[strings.TrimSpace(model.ID)] = struct{}{}
	}
	added := 0
	for _, model := range extra {
		if model == nil {
			continue
		}
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		*base = append(*base, model)
		seen[id] = struct{}{}
		added++
	}
	return added
}
