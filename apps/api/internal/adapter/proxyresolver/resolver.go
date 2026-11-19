// Package proxyresolver turns a worker's `proxy_group` binding into the egress
// configuration its container boots with.
//
// It exists because the binding and the consumer live on opposite sides of the
// provisioner: the group is stored per ACCOUNT (P1-07 binds a pool "at region
// level"), while the proxy is a per-CONTAINER runtime option (Playwright fixes
// it at browser launch). Something has to sit in the middle and answer "what
// proxy does worker X use", and this is that something.
//
// The write-only `pool_key` is the input. The schema documents it only as
// "sealed at rest, never returned" — it does NOT pin a format, so this package
// defines one and rejects everything else loudly rather than guessing at a
// string that would silently route traffic somewhere unintended. A wrong
// egress is not a visible failure: the login just fails, from an IP nobody
// chose.
package proxyresolver

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/ahdirmai/jg-smm/apps/api/internal/adapter/dockerprovisioner"
	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// PoolKeyStore reads the sealed pool key bound to a worker, or
// domain.ErrNotFound when the worker has no group.
type PoolKeyStore interface {
	GetPoolKeyForWorker(ctx context.Context, workerID string) ([]byte, error)
}

// Resolver implements dockerprovisioner.Config.ProxyFor and the pool's
// per-account egress resolution.
type Resolver struct {
	store  PoolKeyStore
	sealer port.Sealer
	// groupStore is the per-ACCOUNT path (pool mode). Set with WithGroups; a
	// resolver built only for the container path leaves it nil.
	groupStore AccountPoolKeyStore
}

// WithGroups enables ForAccount by supplying the proxy-group store.
func (r *Resolver) WithGroups(store AccountPoolKeyStore) *Resolver {
	r.groupStore = store
	return r
}

// New builds the resolver. A nil sealer disables resolution: a stack with no
// credential key configured cannot have sealed pool keys either, so every
// worker dials direct and the caller's nil check handles it.
func New(store PoolKeyStore, sealer port.Sealer) *Resolver {
	return &Resolver{store: store, sealer: sealer}
}

var _ func(ctx context.Context, workerID string) (*dockerprovisioner.WorkerProxy, error) = (*Resolver)(nil).ForWorker

// ForWorker returns the egress for a worker's container, or nil when the worker
// has no proxy group (the direct-dial default, and the normal local state).
//
// Only an absent binding is nil. A binding that exists but cannot be read is an
// error: falling back to direct dial on a decrypt failure would quietly move a
// worker off its pool, which is exactly the datacentre-IP problem the pool was
// bought to fix.
func (r *Resolver) ForWorker(ctx context.Context, workerID string) (*dockerprovisioner.WorkerProxy, error) {
	if r == nil || r.store == nil || r.sealer == nil {
		return nil, nil
	}
	sealed, err := r.store.GetPoolKeyForWorker(ctx, workerID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("proxyresolver: read pool key for worker %s: %w", workerID, err)
	}
	if len(sealed) == 0 {
		return nil, nil
	}
	plain, err := r.sealer.Open(sealed)
	if err != nil {
		return nil, fmt.Errorf("proxyresolver: unseal pool key for worker %s: %w", workerID, err)
	}
	return Parse(string(plain))
}

// Parse decodes the pool_key plaintext into an egress configuration.
//
// Accepted forms, chosen to cover what residential providers actually hand out:
//
//	host:port                       (no scheme; assumed http)
//	scheme://host:port              (http, https, socks5)
//	scheme://user:pass@host:port    (credentials inline)
//
// Anything else — JSON, a bare hostname with no port, a provider-specific blob,
// an empty string — is an error, not a best-effort guess. The failure mode of
// guessing is silent: the container boots, egresses from the datacentre IP the
// pool was meant to hide, and the only symptom is logins that keep failing.
func Parse(poolKey string) (*dockerprovisioner.WorkerProxy, error) {
	raw := strings.TrimSpace(poolKey)
	if raw == "" {
		return nil, errors.New("proxyresolver: pool key is empty")
	}

	// A bare host:port has no scheme, and url.Parse reads "gate.example:1080"
	// as scheme "gate.example" with an opaque path. Add the default explicitly
	// rather than letting that parse succeed into nonsense.
	candidate := raw
	if !strings.Contains(raw, "://") {
		candidate = "http://" + raw
	}
	u, err := url.Parse(candidate)
	if err != nil {
		return nil, fmt.Errorf("proxyresolver: pool key is not a usable proxy address: %w", err)
	}
	if u.Host == "" || u.Hostname() == "" {
		return nil, fmt.Errorf("proxyresolver: pool key %q has no host", raw)
	}
	if u.Port() == "" {
		return nil, fmt.Errorf("proxyresolver: pool key %q has no port", raw)
	}
	switch u.Scheme {
	case "http", "https", "socks5":
	default:
		return nil, fmt.Errorf("proxyresolver: unsupported proxy scheme %q (want http, https or socks5)", u.Scheme)
	}

	out := &dockerprovisioner.WorkerProxy{
		Server: fmt.Sprintf("%s://%s", u.Scheme, u.Host),
	}
	if u.User != nil {
		out.Username = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			out.Password = pw
		}
	}
	return out, nil
}
