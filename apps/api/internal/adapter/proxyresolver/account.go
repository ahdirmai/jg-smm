package proxyresolver

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// AccountPoolKeyStore reads the proxy group bound to an account.
type AccountPoolKeyStore interface {
	GetByID(ctx context.Context, id string) (domain.ProxyGroup, error)
}

// ErrSocksAuthUnsupported is returned for a socks5 pool key with credentials.
// Chromium (the Playwright 1.48 build the workers ship) only accepts per-context
// proxy authentication over http/https; handing it socks5+auth silently egresses
// from the worker's own datacentre IP instead of the pool.
var ErrSocksAuthUnsupported = errors.New(
	"socks5 proxy with authentication is not supported by Chromium BrowserContext; use http:// or https://")

// accountPlaceholder matches the sticky-session placeholders providers document.
var accountPlaceholder = regexp.MustCompile(`\{(account_id|session)\}`)

// ForAccount resolves the egress for ONE account (stateless pool mode).
//
// Unlike ForWorker, the proxy belongs to the account, not the machine: a boxd
// worker is shared and disposable, so a per-context proxy is what keeps each
// account on its own residential IP. nil (no error) means the account has no
// proxy group and the caller must pin it to a stable worker instead — a
// proxyless account that hops machines presents a new egress IP per job.
func (r *Resolver) ForAccount(ctx context.Context, accountID string, proxyGroupID *string) (*port.ProxySpec, error) {
	if r == nil || r.sealer == nil || proxyGroupID == nil || *proxyGroupID == "" {
		return nil, nil
	}
	group, err := r.groupStore.GetByID(ctx, *proxyGroupID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("proxyresolver: read proxy group for account %s: %w", accountID, err)
	}
	if len(group.PoolKeyEnc) == 0 {
		return nil, nil
	}
	plain, err := r.sealer.Open(group.PoolKeyEnc)
	if err != nil {
		return nil, fmt.Errorf("proxyresolver: unseal pool key for account %s: %w", accountID, err)
	}
	// Substitute BEFORE parsing: "{}" is illegal in URL userinfo, so a sticky
	// key only becomes a parseable URL once the placeholder is replaced.
	raw := accountPlaceholder.ReplaceAllString(string(plain), stickyToken(accountID))
	route, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	spec := &port.ProxySpec{Server: route.Server, Username: route.Username, Password: route.Password}
	if err := validate(spec); err != nil {
		return nil, fmt.Errorf("proxyresolver: account %s: %w", accountID, err)
	}
	return spec, nil
}

// stickyToken is 12 lowercase-alphanumeric characters of the account id, with
// separators removed so the value is safe unescaped in a proxy username.
func stickyToken(accountID string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(accountID) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			if b.Len() == 12 {
				break
			}
		}
	}
	out := b.String()
	for len(out) < 12 {
		out += "0"
	}
	return out
}

// validate refuses a socks5 route that carries credentials: Playwright would
// accept the config and Chromium would ignore the auth, which is worse than an
// explicit error because the egress silently falls back to the datacentre.
func validate(spec *port.ProxySpec) error {
	if strings.HasPrefix(strings.ToLower(spec.Server), "socks5:") &&
		(spec.Username != "" || spec.Password != "") {
		return ErrSocksAuthUnsupported
	}
	return nil
}
