package domain

import "testing"

// P3-12 error classification. The classifier is a pure substring switch, so a
// table is the whole proof: every documented class resolves, precedence is
// right (BANNED beats AUTH beats RATE_LIMIT beats TRANSIENT), and an
// unrecognised message lands on UNKNOWN (not retryable).

func TestClassifyActionError(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want ErrorClass
	}{
		// BANNED: never retried — the account is gone, another attempt cannot
		// fix it.
		{"banned", "Your account has been banned", ErrorClassBanned},
		{"suspended", "Account suspended for violating terms", ErrorClassBanned},
		{"action blocked", "Action blocked: try again later", ErrorClassBanned},
		{"disabled", "This account has been permanently disabled", ErrorClassBanned},

		// AUTH: never retried — the session is gone, re-login is a control
		// flow, not a retry.
		{"login required", "Please log in to continue", ErrorClassAuth},
		{"checkpoint", "Checkpoint required: verify it's you", ErrorClassAuth},
		{"2fa", "Enter the 2FA code", ErrorClassAuth},
		{"bad password", "Sorry, your password is incorrect", ErrorClassAuth},

		// RATE_LIMIT: retried with backoff.
		{"429", "HTTP 429 Too Many Requests", ErrorClassRateLimit},
		{"rate limit", "You have hit the rate limit", ErrorClassRateLimit},
		{"try again later", "Please try again later", ErrorClassRateLimit},

		// TRANSIENT: retried, no session change.
		{"timeout", "page.goto: Timeout 30000ms exceeded", ErrorClassTransient},
		{"selector", "Error: element not found [data-testid='comment']", ErrorClassTransient},
		{"network", "net::ERR_CONNECTION_RESET", ErrorClassTransient},

		// Precedence: a banned account often presents as a login wall first.
		// If BANNED were tested after AUTH this would misclassify as AUTH and
		// waste a retry.
		{"banned beats auth", "login required: your account has been banned", ErrorClassBanned},
		{"rate limit beats transient", "timeout: try again in a few minutes (429)", ErrorClassRateLimit},

		// Unknown failures are not retried by default.
		{"unknown", "something unexpected happened", ErrorClassUnknown},
		{"empty", "", ErrorClassUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyActionError(tc.msg); got != tc.want {
				t.Fatalf("ClassifyActionError(%q) = %q, want %q", tc.msg, got, tc.want)
			}
		})
	}
}

// TestClassifyRetryable pins the retry policy to the class: only the two
// recoverable classes retry, and AUTH/BANNED do not — re-running an action on a
// dead session or a locked account cannot produce a different result.
func TestClassifyRetryable(t *testing.T) {
	retryable := map[ErrorClass]bool{
		ErrorClassTransient: true,
		ErrorClassRateLimit: true,
		ErrorClassAuth:      false,
		ErrorClassBanned:    false,
		ErrorClassUnknown:   false,
	}
	for _, c := range AllErrorClasses {
		if got := c.Retryable(); got != retryable[c] {
			t.Fatalf("Retryable(%q) = %v, want %v", c, got, retryable[c])
		}
	}
}

// TestClassifyValid covers the closed set the schema CHECK enforces, so a class
// the classifier emits can always be stored.
func TestClassifyValid(t *testing.T) {
	for _, tc := range []string{
		"banned", "login required", "429", "timeout", "mystery",
	} {
		if got := ClassifyActionError(tc); !got.Valid() {
			t.Fatalf("class %q from %q is not schema-valid", got, tc)
		}
	}
}
