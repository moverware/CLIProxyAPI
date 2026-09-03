package auth

import (
	"context"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func lastResortTestAuth(id string, weight int64) *Auth {
	return &Auth{
		ID: id, Provider: "claude", Status: StatusActive,
		Metadata:   map[string]any{"weight": weight},
		Attributes: map[string]string{AttributeLastResort: "true"},
	}
}

func TestWeightedPickSkipsLastResortWhileOrdinaryAvailable(t *testing.T) {
	selector := &WeightedRoundRobinSelector{}
	sub := headroomTestAuth("sub", 1, false)
	key := lastResortTestAuth("key", 1_000_000)
	for i := 0; i < 5; i++ {
		picked, errPick := selector.Pick(context.Background(), "claude", "m", cliproxyexecutor.Options{}, []*Auth{sub, key})
		if errPick != nil {
			t.Fatalf("pick: %v", errPick)
		}
		if picked.ID != "sub" {
			t.Fatalf("pick = %q, want the ordinary credential despite the key's higher weight", picked.ID)
		}
	}
}

func TestWeightedPickUsesLastResortBeforeParked(t *testing.T) {
	now := time.Now()
	selector := &WeightedRoundRobinSelector{}
	sub := headroomTestAuth("sub", 1_000_000, false)
	blockAuthWithCooldown(sub, now)
	parked := headroomTestAuth("parked", 1_000_000, true)
	key := lastResortTestAuth("key", 1)
	picked, errPick := selector.Pick(context.Background(), "claude", "m", cliproxyexecutor.Options{}, []*Auth{sub, parked, key})
	if errPick != nil {
		t.Fatalf("pick: %v", errPick)
	}
	if picked.ID != "key" {
		t.Fatalf("pick = %q, want the last-resort key ahead of the headroom-parked tail", picked.ID)
	}
}

func TestWeightedPickDipsIntoParkedWhenLastResortBlocked(t *testing.T) {
	now := time.Now()
	selector := &WeightedRoundRobinSelector{}
	sub := headroomTestAuth("sub", 1, false)
	blockAuthWithCooldown(sub, now)
	key := lastResortTestAuth("key", 1)
	blockAuthWithCooldown(key, now)
	parked := headroomTestAuth("parked", 1, true)
	picked, errPick := selector.Pick(context.Background(), "claude", "m", cliproxyexecutor.Options{}, []*Auth{sub, key, parked})
	if errPick != nil {
		t.Fatalf("pick: %v", errPick)
	}
	if picked.ID != "parked" {
		t.Fatalf("pick = %q, want the parked tail once the key is blocked too", picked.ID)
	}
}

func TestAffinityEvictsPinFromLastResortOnRecovery(t *testing.T) {
	now := time.Now()
	selector := newHeadroomAffinitySelector(t)
	sub := headroomTestAuth("sub", 1_000_000, false)
	blockAuthWithCooldown(sub, now)
	key := lastResortTestAuth("key", 1)
	opts := headroomAffinityOpts("overflow-session")

	picked, errPick := selector.Pick(context.Background(), "claude", "m", opts, []*Auth{sub, key})
	if errPick != nil {
		t.Fatalf("overflow pick: %v", errPick)
	}
	if picked.ID != "key" {
		t.Fatalf("overflow pick = %q, want %q while the subscription is blocked", picked.ID, "key")
	}

	sub.Unavailable = false
	sub.Quota = QuotaState{}
	sub.NextRetryAfter = time.Time{}
	picked, errPick = selector.Pick(context.Background(), "claude", "m", opts, []*Auth{sub, key})
	if errPick != nil {
		t.Fatalf("recovery pick: %v", errPick)
	}
	if picked.ID != "sub" {
		t.Fatalf("recovery pick = %q, want the pin evicted back to %q", picked.ID, "sub")
	}
}

func TestDisabledLastResortNeverServes(t *testing.T) {
	now := time.Now()
	selector := &WeightedRoundRobinSelector{}
	sub := headroomTestAuth("sub", 1, false)
	blockAuthWithCooldown(sub, now)
	key := lastResortTestAuth("key", 1)
	key.Disabled = true
	if _, errPick := selector.Pick(context.Background(), "claude", "m", cliproxyexecutor.Options{}, []*Auth{sub, key}); errPick == nil {
		t.Fatal("pick succeeded, want an error — a disabled last-resort key must not serve")
	}
}
