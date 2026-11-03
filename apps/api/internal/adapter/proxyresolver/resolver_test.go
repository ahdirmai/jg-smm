package proxyresolver

import "testing"

// Parse is the one place a `pool_key` becomes an egress route, and a wrong
// answer is silent: the container boots, reports the wrong IP, and the only
// symptom is logins that keep failing. So the accepted forms are pinned here
// and everything else must be refused rather than guessed at.
func TestParseAcceptsProviderForms(t *testing.T) {
	cases := []struct {
		name     string
		poolKey  string
		wantSrvr string
		wantUser string
		wantPass string
	}{
		{"bare host:port defaults to http", "gate.example.com:8080", "http://gate.example.com:8080", "", ""},
		{"socks5", "socks5://gate.example.com:1080", "socks5://gate.example.com:1080", "", ""},
		{"https", "https://gate.example.com:8443", "https://gate.example.com:8443", "", ""},
		{
			"inline credentials",
			"http://pooluser:poolpass@gate.example.com:8080",
			"http://gate.example.com:8080", "pooluser", "poolpass",
		},
		{"surrounding whitespace", "  http://gate.example.com:8080\n", "http://gate.example.com:8080", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.poolKey)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.poolKey, err)
			}
			if got.Server != tc.wantSrvr {
				t.Errorf("server = %q, want %q", got.Server, tc.wantSrvr)
			}
			if got.Username != tc.wantUser || got.Password != tc.wantPass {
				t.Errorf("credentials = %q/%q, want %q/%q",
					got.Username, got.Password, tc.wantUser, tc.wantPass)
			}
		})
	}
}

// A rejected key must stay rejected. Each of these would otherwise route
// traffic somewhere the operator did not choose, or nowhere at all.
func TestParseRejectsUnusableKeys(t *testing.T) {
	cases := []struct {
		name    string
		poolKey string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"no port", "gate.example.com"},
		{"no host", "http://:8080"},
		{"unsupported scheme", "ftp://gate.example.com:21"},
		{"provider-specific blob", `{"host":"gate.example.com","port":8080}`},
		{"unparseable", "http://[::1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := Parse(tc.poolKey); err == nil {
				t.Errorf("Parse(%q) = %+v, want an error", tc.poolKey, got)
			}
		})
	}
}

// A nil resolver is the direct-dial default, and it must not be a nil-pointer
// panic on the provisioning path — that would take the create down instead of
// quietly dialling direct.
func TestNilResolverDialsDirect(t *testing.T) {
	var r *Resolver
	got, err := r.ForWorker(t.Context(), "worker-1")
	if err != nil {
		t.Fatalf("ForWorker on nil resolver: %v", err)
	}
	if got != nil {
		t.Errorf("ForWorker on nil resolver = %+v, want nil (direct dial)", got)
	}
}
