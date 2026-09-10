package auth

import (
	"context"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func headroomTestAuth(id string, weight int64, parked bool) *Auth {
	metadata := map[string]any{"weight": weight}
	if parked {
		metadata["headroom_parked"] = true
	}
	return &Auth{ID: id, Provider: "claude", Status: StatusActive, Metadata: metadata}
}

func blockAuthWithCooldown(auth *Auth, now time.Time) {
	auth.Unavailable = true
	auth.Quota = QuotaState{Exceeded: true, NextRecoverAt: now.Add(time.Hour)}
	auth.NextRetryAfter = now.Add(time.Hour)
}

func headroomAffinityOpts(session string) cliproxyexecutor.Options {
	return cliproxyexecutor.Options{Metadata: map[string]any{
		cliproxyexecutor.DerivedSessionIDMetadataKey: session,
	}}
}

func newHeadroomAffinitySelector(t *testing.T) *SessionAffinitySelector {
	t.Helper()
	selector := NewSessionAffinitySelectorWithConfig(SessionAffinityConfig{
		Fallback: &WeightedRoundRobinSelector{},
		TTL:      time.Hour,
	})
	t.Cleanup(selector.Stop)
	return selector
}

func TestWeightedPickExcludesParkedWhileUnparkedAvailable(t *testing.T) {
	selector := &WeightedRoundRobinSelector{}
	fresh := headroomTestAuth("fresh", 1, false)
	parked := headroomTestAuth("parked", 1_000_000, true)
	for i := 0; i < 5; i++ {
		picked, errPick := selector.Pick(context.Background(), "claude", "m", cliproxyexecutor.Options{}, []*Auth{fresh, parked})
		if errPick != nil {
			t.Fatalf("pick: %v", errPick)
		}
		if picked.ID != "fresh" {
			t.Fatalf("pick = %q, want the unparked credential despite its lower weight", picked.ID)
		}
	}
}

func TestWeightedPickDipsIntoParkedWhenUnparkedBlocked(t *testing.T) {
	now := time.Now()
	selector := &WeightedRoundRobinSelector{}
	fresh := headroomTestAuth("fresh", 1_000_000, false)
	blockAuthWithCooldown(fresh, now)
	parkedSooner := headroomTestAuth("parked-sooner", 1_000, true)
	parkedLater := headroomTestAuth("parked-later", 1, true)
	picked, errPick := selector.Pick(context.Background(), "claude", "m", cliproxyexecutor.Options{}, []*Auth{fresh, parkedSooner, parkedLater})
	if errPick != nil {
		t.Fatalf("pick: %v", errPick)
	}
	if picked.ID != "parked-sooner" {
		t.Fatalf("dip pick = %q, want the highest-weighted parked credential", picked.ID)
	}
}

func TestAffinityEvictsPinFromParkedCredential(t *testing.T) {
	selector := newHeadroomAffinitySelector(t)
	hot := headroomTestAuth("hot", 1_000_000, false)
	other := headroomTestAuth("other", 1, false)
	opts := headroomAffinityOpts("pinned-session")

	picked, errPick := selector.Pick(context.Background(), "claude", "m", opts, []*Auth{hot, other})
	if errPick != nil {
		t.Fatalf("cold pick: %v", errPick)
	}
	if picked.ID != "hot" {
		t.Fatalf("cold pick = %q, want %q", picked.ID, "hot")
	}

	hot.Metadata["headroom_parked"] = true
	picked, errPick = selector.Pick(context.Background(), "claude", "m", opts, []*Auth{hot, other})
	if errPick != nil {
		t.Fatalf("post-park pick: %v", errPick)
	}
	if picked.ID != "other" {
		t.Fatalf("post-park pick = %q, want the pin evicted to %q", picked.ID, "other")
	}
}

func TestAffinityDipReturnsToUnparkedOnRecovery(t *testing.T) {
	now := time.Now()
	selector := newHeadroomAffinitySelector(t)
	fresh := headroomTestAuth("fresh", 1_000_000, false)
	blockAuthWithCooldown(fresh, now)
	parked := headroomTestAuth("parked", 1, true)
	opts := headroomAffinityOpts("dip-session")

	picked, errPick := selector.Pick(context.Background(), "claude", "m", opts, []*Auth{fresh, parked})
	if errPick != nil {
		t.Fatalf("dip pick: %v", errPick)
	}
	if picked.ID != "parked" {
		t.Fatalf("dip pick = %q, want %q while everything unparked is blocked", picked.ID, "parked")
	}

	fresh.Unavailable = false
	fresh.Quota = QuotaState{}
	fresh.NextRetryAfter = time.Time{}
	picked, errPick = selector.Pick(context.Background(), "claude", "m", opts, []*Auth{fresh, parked})
	if errPick != nil {
		t.Fatalf("recovery pick: %v", errPick)
	}
	if picked.ID != "fresh" {
		t.Fatalf("recovery pick = %q, want the dip pin evicted back to %q", picked.ID, "fresh")
	}
}

func TestDisabledParkedCredentialNeverCatchesTheDip(t *testing.T) {
	now := time.Now()
	selector := &WeightedRoundRobinSelector{}
	fresh := headroomTestAuth("fresh", 1_000_000, false)
	blockAuthWithCooldown(fresh, now)
	parkedDisabled := headroomTestAuth("parked-disabled", 1_000, true)
	parkedDisabled.Disabled = true
	if _, errPick := selector.Pick(context.Background(), "claude", "m", cliproxyexecutor.Options{}, []*Auth{fresh, parkedDisabled}); errPick == nil {
		t.Fatal("pick succeeded, want an error — a manually disabled credential must not serve the dip")
	}
}

func headroomReservedTestAuth(id string, weight int64) *Auth {
	auth := headroomTestAuth(id, weight, true)
	auth.Metadata["headroom_no_dip"] = true
	return auth
}

func TestWeightedPickNeverDipsIntoReservedCredential(t *testing.T) {
	now := time.Now()
	selector := &WeightedRoundRobinSelector{}
	fresh := headroomTestAuth("fresh", 1_000_000, false)
	blockAuthWithCooldown(fresh, now)
	reserved := headroomReservedTestAuth("reserved", 1_000_000)
	parked := headroomTestAuth("parked", 1, true)
	picked, errPick := selector.Pick(context.Background(), "claude", "m", cliproxyexecutor.Options{}, []*Auth{fresh, reserved, parked})
	if errPick != nil {
		t.Fatalf("dip pick: %v", errPick)
	}
	if picked.ID != "parked" {
		t.Fatalf("dip pick = %q, want the ordinary parked credential; the reserved one must never catch the dip", picked.ID)
	}
	if _, errPick = selector.Pick(context.Background(), "claude", "m", cliproxyexecutor.Options{}, []*Auth{fresh, reserved}); errPick == nil {
		t.Fatal("pick succeeded, want an error — a reserved credential is the only servable one and must still not serve")
	}
}

func TestReservedCredentialServesNormallyWhileUnparked(t *testing.T) {
	selector := &WeightedRoundRobinSelector{}
	only := headroomTestAuth("only", 1, false)
	only.Metadata["headroom_no_dip"] = true
	picked, errPick := selector.Pick(context.Background(), "claude", "m", cliproxyexecutor.Options{}, []*Auth{only})
	if errPick != nil {
		t.Fatalf("pick: %v", errPick)
	}
	if picked.ID != "only" {
		t.Fatalf("pick = %q, want the no-dip credential to serve while it is not parked", picked.ID)
	}
}

func TestAffinityEvictsPinFromReservedCredentialEvenWhenAllElseBlocked(t *testing.T) {
	now := time.Now()
	selector := newHeadroomAffinitySelector(t)
	hot := headroomTestAuth("hot", 1_000_000, false)
	hot.Metadata["headroom_no_dip"] = true
	other := headroomTestAuth("other", 1, false)
	opts := headroomAffinityOpts("reserved-session")

	picked, errPick := selector.Pick(context.Background(), "claude", "m", opts, []*Auth{hot, other})
	if errPick != nil {
		t.Fatalf("cold pick: %v", errPick)
	}
	if picked.ID != "hot" {
		t.Fatalf("cold pick = %q, want %q", picked.ID, "hot")
	}

	hot.Metadata["headroom_parked"] = true
	blockAuthWithCooldown(other, now)
	if _, errPick = selector.Pick(context.Background(), "claude", "m", opts, []*Auth{hot, other}); errPick == nil {
		t.Fatal("pick succeeded, want an error — the pin must be evicted from the reserved credential and nothing else can serve")
	}
}
