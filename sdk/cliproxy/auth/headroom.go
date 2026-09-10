package auth

// FORK: headroom parking for the account pool. The llm-dashboard writes a
// top-level `headroom_parked` field into an auth file when that account's
// weekly meter crosses the headroom cap — the last ~10% of each
// subscription is kept for use from devices outside the proxy. A parked
// credential takes no traffic while anything on a higher rung can serve;
// it is the final rung of the fallback order in last_resort.go, dipped
// into only when every unparked and every last-resort credential is
// blocked (5h cooldown, weekly exhaustion). Manually disabled credentials
// never serve either way — isAuthBlockedForModel blocks them in the
// availability probe and again in selection.
//
// A parked credential whose auth file also carries `headroom_no_dip`
// is reserved outright: it leaves routing entirely while parked and is
// never dipped into, even when every other rung is blocked. The flag
// marks a subscription whose headroom belongs to its owner's own devices
// (a personal phone account) rather than to the pool's overflow.

import (
	"strconv"
	"strings"
)

func authHeadroomParked(auth *Auth) bool {
	return authMetadataFlag(auth, "headroom_parked")
}

// authHeadroomReserved reports a parked credential that must never catch
// the dip: parked AND flagged headroom_no_dip.
func authHeadroomReserved(auth *Auth) bool {
	return authHeadroomParked(auth) && authMetadataFlag(auth, "headroom_no_dip")
}

func authMetadataFlag(auth *Auth, key string) bool {
	if auth == nil || auth.Metadata == nil {
		return false
	}
	switch v := auth.Metadata[key].(type) {
	case bool:
		return v
	case string:
		parsed, errParse := strconv.ParseBool(strings.TrimSpace(v))
		return errParse == nil && parsed
	}
	return false
}
