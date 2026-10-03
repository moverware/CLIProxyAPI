package auth

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func ultrafastTestAuths(disabled bool) []*Auth {
	return []*Auth{
		{ID: "pro-max", Provider: "codex", Disabled: disabled, Attributes: map[string]string{AttributeAuthKind: AuthKindOAuth, "plan_type": "promax", AttributeWeight: "1"}},
		{ID: "pro-five", Provider: "codex", Attributes: map[string]string{AttributeAuthKind: AuthKindOAuth, "plan_type": "pro", AttributeWeight: "100"}},
		{ID: "team", Provider: "codex", Attributes: map[string]string{AttributeAuthKind: AuthKindOAuth, "plan_type": "team", AttributeWeight: "100"}},
		{ID: "unknown-plan", Provider: "codex", Attributes: map[string]string{AttributeAuthKind: AuthKindOAuth, AttributeWeight: "100"}},
		{ID: "codex-api", Provider: "codex", Attributes: map[string]string{AttributeAuthKind: AuthKindAPIKey, AttributeWeight: "100"}},
		{ID: "metered-api", Provider: "openai-compatibility", Attributes: map[string]string{AttributeAuthKind: AuthKindAPIKey, AttributeWeight: "100"}},
	}
}

func registerUltrafastTestAuths(t *testing.T, manager *Manager, disabled bool) {
	t.Helper()
	manager.executors["codex"] = schedulerTestExecutor{provider: "codex"}
	manager.executors["openai-compatibility"] = schedulerTestExecutor{provider: "openai-compatibility"}
	for _, candidate := range ultrafastTestAuths(disabled) {
		if _, errRegister := manager.Register(WithSkipPersist(context.Background()), candidate); errRegister != nil {
			t.Fatalf("Register(%s): %v", candidate.ID, errRegister)
		}
	}
}

func TestManagerUltrafastEligibilityAcrossSchedulers(t *testing.T) {
	for _, mode := range []string{"weighted", "round-robin", "affinity", "plugin"} {
		for _, mixed := range []bool{false, true} {
			for _, disabled := range []bool{false, true} {
				t.Run(mode+"/mixed="+strconv.FormatBool(mixed)+"/disabled="+strconv.FormatBool(disabled), func(t *testing.T) {
					manager := NewManager(nil, &WeightedRoundRobinSelector{}, nil)
					registerUltrafastTestAuths(t, manager, disabled)
					var plugin *fakePluginScheduler
					switch mode {
					case "round-robin":
						manager.SetSelector(&RoundRobinSelector{})
					case "affinity":
						affinity := NewSessionAffinitySelectorWithConfig(SessionAffinityConfig{Fallback: &WeightedRoundRobinSelector{}, TTL: time.Hour})
						t.Cleanup(affinity.Stop)
						manager.SetSelector(affinity)
					case "plugin":
						plugin = &fakePluginScheduler{pick: func(_ context.Context, req pluginapi.SchedulerPickRequest) (pluginapi.SchedulerPickResponse, bool, error) {
							selectedID := ""
							largestWeight := -1
							for _, candidate := range req.Candidates {
								weight, _ := strconv.Atoi(candidate.Attributes[AttributeWeight])
								if weight > largestWeight {
									selectedID, largestWeight = candidate.ID, weight
								}
							}
							return pluginapi.SchedulerPickResponse{Handled: true, AuthID: selectedID}, true, nil
						}}
						manager.SetPluginScheduler(plugin)
					}
					opts := cliproxyexecutor.Options{Metadata: map[string]any{
						cliproxyexecutor.ServiceTierMetadataKey:      "ultrafast",
						cliproxyexecutor.DerivedSessionIDMetadataKey: "synthetic-session",
					}}
					for attempt := 0; attempt < 12; attempt++ {
						var selected *Auth
						var errPick error
						if mixed {
							selected, _, _, errPick = manager.pickNextMixed(context.Background(), []string{"codex", "openai-compatibility"}, "", opts, nil)
						} else {
							selected, _, errPick = manager.pickNext(context.Background(), "codex", "", opts, nil)
						}
						if disabled {
							var authErr *Error
							if selected != nil || !errors.As(errPick, &authErr) || authErr.Code != "auth_not_found" {
								t.Fatalf("unavailable ultrafast pick = %v, %v; want auth_not_found", selected, errPick)
							}
						} else if errPick != nil || selected == nil || selected.ID != "pro-max" {
							t.Fatalf("ultrafast pick = %v, %v; want pro-max", selected, errPick)
						}
					}
					if plugin != nil && !disabled {
						for _, request := range plugin.requests {
							if len(request.Candidates) != 1 || request.Candidates[0].ID != "pro-max" {
								t.Fatalf("plugin candidates = %v; want only pro-max", request.Candidates)
							}
						}
					}
				})
			}
		}
	}
}

func TestManagerUltrafastExplicitAPISelection(t *testing.T) {
	manager := NewManager(nil, &WeightedRoundRobinSelector{}, nil)
	registerUltrafastTestAuths(t, manager, true)
	opts := cliproxyexecutor.Options{Metadata: map[string]any{cliproxyexecutor.ServiceTierMetadataKey: "ultrafast"}}
	selected, errSelect := manager.SelectAuthByKind(context.Background(), "codex", "", AuthKindAPIKey, opts)
	if errSelect != nil || selected == nil || selected.ID != "codex-api" {
		t.Fatalf("explicit API route = %v, %v; want codex-api", selected, errSelect)
	}
	for _, authID := range []string{"codex-api", "metered-api", "pro-five", "team"} {
		opts.Metadata[cliproxyexecutor.PinnedAuthMetadataKey] = authID
		selected, _, _, errPick := manager.pickNextMixed(context.Background(), []string{"codex", "openai-compatibility"}, "", opts, nil)
		if authID == "codex-api" || authID == "metered-api" {
			if errPick != nil || selected == nil || selected.ID != authID {
				t.Fatalf("explicit API pin = %v, %v; want %s", selected, errPick, authID)
			}
		} else if selected != nil || errPick == nil {
			t.Fatalf("ineligible OAuth pin = %v, %v; want rejection", selected, errPick)
		}
	}
}

func TestManagerPriorityKeepsSharedPool(t *testing.T) {
	manager := NewManager(nil, &WeightedRoundRobinSelector{}, nil)
	registerUltrafastTestAuths(t, manager, true)
	opts := cliproxyexecutor.Options{Metadata: map[string]any{cliproxyexecutor.ServiceTierMetadataKey: "priority"}}
	counts := make(map[string]int)
	for attempt := 0; attempt < 40; attempt++ {
		selected, _, errPick := manager.pickNext(context.Background(), "codex", "", opts, nil)
		if errPick != nil || selected == nil {
			t.Fatalf("priority pick = %v, %v", selected, errPick)
		}
		counts[selected.ID]++
	}
	for _, authID := range []string{"pro-five", "team", "unknown-plan", "codex-api"} {
		if counts[authID] != 10 {
			t.Fatalf("priority shared pool counts = %v; want 10 per available credential", counts)
		}
	}
}

func TestManagerUltrafastRetriesDoNotUseIneligibleCredentials(t *testing.T) {
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	registerUltrafastTestAuths(t, manager, true)
	opts := cliproxyexecutor.Options{Metadata: map[string]any{cliproxyexecutor.ServiceTierMetadataKey: "ultrafast"}}
	eligibility := authSelectionEligibilityForRequest(context.Background(), opts)
	providers := []string{"codex", "openai-compatibility"}
	if manager.retryAllowed(0, providers, "", eligibility, "", 3) {
		t.Fatal("ineligible credentials enabled an ultrafast retry")
	}
	if _, found := manager.closestCooldownWait(providers, "", 0, eligibility, "", 3); found {
		t.Fatal("ineligible credentials supplied an ultrafast retry deadline")
	}
}

func TestManagerUltrafastReplacesIneligibleSessionBinding(t *testing.T) {
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	registerUltrafastTestAuths(t, manager, false)
	affinity := NewSessionAffinitySelectorWithConfig(SessionAffinityConfig{Fallback: &RoundRobinSelector{}, TTL: time.Hour})
	defer affinity.Stop()
	manager.SetSelector(affinity)
	opts := cliproxyexecutor.Options{Metadata: map[string]any{
		cliproxyexecutor.DerivedSessionIDMetadataKey: "existing-session",
		cliproxyexecutor.PinnedAuthMetadataKey:       "pro-five",
		cliproxyexecutor.ServiceTierMetadataKey:      "priority",
	}}
	selected, _, errPick := manager.pickNext(context.Background(), "codex", "", opts, nil)
	if errPick != nil || selected == nil || selected.ID != "pro-five" {
		t.Fatalf("initial session binding = %v, %v; want pro-five", selected, errPick)
	}
	delete(opts.Metadata, cliproxyexecutor.PinnedAuthMetadataKey)
	opts.Metadata[cliproxyexecutor.ServiceTierMetadataKey] = "ultrafast"
	selected, _, errPick = manager.pickNext(context.Background(), "codex", "", opts, nil)
	if errPick != nil || selected == nil || selected.ID != "pro-max" {
		t.Fatalf("ultrafast session binding = %v, %v; want pro-max", selected, errPick)
	}
}

func TestManagerUltrafastCooldownCannotSpillToSharedPool(t *testing.T) {
	manager := NewManager(nil, &WeightedRoundRobinSelector{}, nil)
	registerUltrafastTestAuths(t, manager, false)
	candidate := manager.auths["pro-max"].Clone()
	candidate.Unavailable = true
	candidate.Quota = QuotaState{Exceeded: true, Reason: "credential_quota", NextRecoverAt: time.Now().Add(time.Minute)}
	candidate.NextRetryAfter = candidate.Quota.NextRecoverAt
	if _, errUpdate := manager.Update(WithSkipPersist(context.Background()), candidate); errUpdate != nil {
		t.Fatalf("Update(pro-max): %v", errUpdate)
	}
	opts := cliproxyexecutor.Options{Metadata: map[string]any{cliproxyexecutor.ServiceTierMetadataKey: "ultrafast"}}
	selected, _, _, errPick := manager.pickNextMixed(context.Background(), []string{"codex", "openai-compatibility"}, "", opts, nil)
	var cooldown *modelCooldownError
	if selected != nil || !errors.As(errPick, &cooldown) {
		t.Fatalf("ultrafast cooldown pick = %v, %v; want cooldown without shared-pool fallback", selected, errPick)
	}
}
