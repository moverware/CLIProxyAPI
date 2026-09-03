package auth

// FORK: last-resort credentials for the account pool. A credential whose
// attributes carry last_resort=true (a claude-api-key entry with
// `last-resort: true` in config.yaml) is a metered backstop — typically a
// pay-per-token API key sitting behind a set of flat-rate subscription
// accounts. It takes no traffic, new sessions or established
// session-affinity pins alike, while any ordinary unparked credential can
// serve the model. Ordering of the fallback rungs when everything ahead is
// blocked (5h cooldown, weekly exhaustion, disabled):
//
//	1. ordinary credentials that are not headroom-parked
//	2. last-resort credentials
//	3. headroom-parked credentials (the reserved subscription tail)
//
// The last-resort rung sits ahead of the headroom dip on purpose: the
// parked tail exists for use off the proxy, and a metered key is the thing
// meant to absorb overflow. Disabled credentials never serve on any rung.

import (
	"strconv"
	"strings"
	"time"
)

const AttributeLastResort = "last_resort"

func authLastResort(auth *Auth) bool {
	if auth == nil || auth.Attributes == nil {
		return false
	}
	raw := strings.TrimSpace(auth.Attributes[AttributeLastResort])
	if raw == "" {
		return false
	}
	parsed, errParse := strconv.ParseBool(raw)
	return errParse == nil && parsed
}

func anyServable(auths []*Auth, model string, now time.Time) bool {
	for _, candidate := range auths {
		if blocked, _, _ := isAuthBlockedForModel(candidate, model, now); !blocked {
			return true
		}
	}
	return false
}

// routableAuths narrows candidates to the first rung that can serve the
// model right now: ordinary unparked credentials, then last-resort ones,
// then the headroom-parked set. When no rung can serve, the full set is
// returned so the caller reports the pool-wide cooldown as before. Session
// affinity validates pins against the same set, so a pin to a lower rung is
// evicted the moment a higher rung can serve again.
func routableAuths(auths []*Auth, model string, now time.Time) []*Auth {
	ordinary := make([]*Auth, 0, len(auths))
	lastResort := make([]*Auth, 0)
	parked := make([]*Auth, 0)
	for _, candidate := range auths {
		switch {
		case authHeadroomParked(candidate):
			parked = append(parked, candidate)
		case authLastResort(candidate):
			lastResort = append(lastResort, candidate)
		default:
			ordinary = append(ordinary, candidate)
		}
	}
	if len(lastResort) == 0 && len(parked) == 0 {
		return auths
	}
	if anyServable(ordinary, model, now) {
		return ordinary
	}
	if anyServable(lastResort, model, now) {
		return lastResort
	}
	if anyServable(parked, model, now) {
		return parked
	}
	return auths
}
