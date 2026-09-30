package domain

import (
	"strings"
)

// ClassifyActionError (P3-12) turns a worker-reported failure into one of the
// closed error classes the retry policy and the dashboard consume. The input is
// free text (a Playwright message or a platform string), so this is a
// substring switch, deliberately coarse: the operator needs "the account was
// banned" vs "the network hiccuped", not a taxonomy, and a retry policy only
// ever branches four ways.
//
// Order matters and is not alphabetical: BANNED is tested before AUTH, because
// a banned account usually reports as "login required" first, and treating it
// as AUTH would waste a retry on a session that cannot be saved. RATE_LIMIT
// before TRANSIENT, because a throttle presents as a generic timeout once the
// request actually lands.
func ClassifyActionError(msg string) ErrorClass {
	m := strings.ToLower(msg)
	switch {
	case containsAny(m,
		"banned", "suspended", "permanently disabled", "account disabled",
		"locked for violating", "violated our terms", "action blocked",
		"your account has been",
	):
		return ErrorClassBanned
	case containsAny(m,
		"login required", "session expired", "please log in", "not logged in",
		"checkpoint", "two-factor", "2fa", "verify it's you", "security code",
		"incorrect password", "password is incorrect", "invalid credentials", "authenticate",
	):
		return ErrorClassAuth
	case containsAny(m,
		"rate limit", "too many requests", "429", "try again later",
		"slow down", "spam", "few minutes", "temporarily blocked",
	):
		return ErrorClassRateLimit
	// A reply whose composer never appears is not a race. The target comment
	// is permanently gone — deleted, or its Reply affordance is not ours to
	// click — so retrying cannot help. It has to be tested BEFORE the
	// TRANSIENT case below, because the message contains "not found" and the
	// retry is actively harmful: H-2 pins the target by comment id, but each
	// attempt re-clicks `.first()` in DOM order, so three retries mean three
	// replies posted to three different comments on a live account.
	case containsAny(m, "reply composer not found"):
		return ErrorClassUnknown
	case containsAny(m,
		"timeout", "timed out", "deadline", "connection", "network",
		"navigation", "selector", "element not found", "detached",
		// Worker-adapter failShot strings. The app's action bar paints a beat
		// after the permalink resolves or a real click lands, so "not found"
		// and "state did not change" are this app's race outcomes too — they
		// belong with the other transient UI races, not in UNKNOWN where the
		// job would sit unretried.
		"not found", "did not change",
	):
		return ErrorClassTransient
	}
	// An unrecognised failure is UNKNOWN, which is not retryable by default:
	// retrying an error we cannot name is how a broken loop hides.
	return ErrorClassUnknown
}

// containsAny reports whether s contains any of the substrings. Case-folded by
// the caller; empty needles never match (a typo in the list must not turn the
// classifier into a catch-all).
func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if n == "" {
			continue
		}
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
