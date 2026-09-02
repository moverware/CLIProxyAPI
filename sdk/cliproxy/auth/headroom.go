package auth

// FORK: headroom parking for the account pool. The llm-dashboard writes a
// top-level `headroom_parked` field into an auth file when that account's
// weekly meter crosses the headroom cap — the last ~10% of each
// subscription is kept for use from devices outside the proxy. A parked
// credential must take no traffic, new sessions or established
// session-affinity pins alike, while any unparked credential can still
// serve; but it stays the fallback of last resort: when every unparked
// credential is blocked (5h cooldown, weekly exhaustion), routing dips
// into the parked set instead of failing the request. Manually disabled
// credentials never serve either way — isAuthBlockedForModel blocks them
// in this filter's availability probe and again in selection.

import (
	"strconv"
	"strings"
	"time"
)

func authHeadroomParked(auth *Auth) bool {
	if auth == nil || auth.Metadata == nil {
		return false
	}
	switch v := auth.Metadata["headroom_parked"].(type) {
	case bool:
		return v
	case string:
		parsed, errParse := strconv.ParseBool(strings.TrimSpace(v))
		return errParse == nil && parsed
	}
	return false
}

// headroomRoutableAuths narrows candidates to the unparked subset while at
// least one unparked credential can serve the model right now; otherwise
// it returns the full set so parked credentials catch the dip. Session
// affinity validates pins against the same set, so a pin to a parked
// credential is evicted the moment anything unparked can serve.
func headroomRoutableAuths(auths []*Auth, model string, now time.Time) []*Auth {
	unparked := make([]*Auth, 0, len(auths))
	parkedSeen := false
	for _, candidate := range auths {
		if authHeadroomParked(candidate) {
			parkedSeen = true
			continue
		}
		unparked = append(unparked, candidate)
	}
	if !parkedSeen {
		return auths
	}
	for _, candidate := range unparked {
		if blocked, _, _ := isAuthBlockedForModel(candidate, model, now); !blocked {
			return unparked
		}
	}
	return auths
}
