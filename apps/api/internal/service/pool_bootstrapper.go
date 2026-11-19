package service

import (
	"context"
	"fmt"
	"hash/fnv"
	"log/slog"
	"math/rand/v2"
	"strings"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// PoolWorkerDef is one configured fleet member, parsed from
// WORKER_POOL_ENDPOINTS:
//
//	name=https://rpc.smm-worker-01.boxd.sh|https://smm-worker-01.boxd.sh/novnc
//
// The novnc part is optional: without it the worker keeps whatever live-view URL
// its row already had.
type PoolWorkerDef struct {
	Name     string
	RPCURL   string
	NoVNCURL string
	Region   string
}

// ParsePoolEndpoints reads the CSV seed. A malformed entry is an error rather
// than a skipped worker: a fleet that silently comes up with nine machines is
// worse than one that refuses to start.
func ParsePoolEndpoints(raw string) ([]PoolWorkerDef, error) {
	var out []PoolWorkerDef
	for entry := range strings.SplitSeq(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, rest, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("worker pool endpoints: %q is not name=url", entry)
		}
		rpc, novnc, _ := strings.Cut(rest, "|")
		if strings.TrimSpace(rpc) == "" {
			return nil, fmt.Errorf("worker pool endpoints: %q has no rpc url", entry)
		}
		out = append(out, PoolWorkerDef{
			Name:     strings.TrimSpace(name),
			RPCURL:   strings.TrimSpace(rpc),
			NoVNCURL: strings.TrimSpace(novnc),
			Region:   "ID",
		})
	}
	return out, nil
}

// DefaultPoolEndpoints names a fleet that was not configured explicitly: one
// worker per slot, addressed the way a local compose network resolves it. On
// boxd WORKER_POOL_ENDPOINTS supplies the real HTTPS URLs; the names (and so the
// worker rows) are identical either way.
func DefaultPoolEndpoints(size int) []PoolWorkerDef {
	if size < 1 {
		size = 1
	}
	out := make([]PoolWorkerDef, 0, size)
	for i := range size {
		name := fmt.Sprintf("smm-worker-%02d", i+1)
		out = append(out, PoolWorkerDef{
			Name:   name,
			RPCURL: "http://" + name + ":8090",
			Region: "ID",
		})
	}
	return out
}

// DefaultLoginEndpoint names the dedicated login machine when the operator
// configured none explicitly. It is a SEPARATE row from the action fleet: login
// is serial and pinned, actions fan out across smm-worker-*.
func DefaultLoginEndpoint(name string) PoolWorkerDef {
	if name == "" {
		name = "smm-login-01"
	}
	return PoolWorkerDef{Name: name, RPCURL: "http://" + name + ":8090", Region: "ID"}
}

// PoolBootstrapper brings a freshly started API in line with the configured
// fleet: every account gets a city/GPS/timezone identity (the migration could
// only inherit those from the containers accounts used to live in), and the
// pool worker rows exist so the slot coordinator has candidates to hand out.
//
// Both steps are idempotent — they run on every boot, not once.
type PoolBootstrapper struct {
	accounts port.AccountStore
	workers  port.WorkerStore
	log      *slog.Logger
}

// NewPoolBootstrapper builds the bootstrapper.
func NewPoolBootstrapper(accounts port.AccountStore, workers port.WorkerStore, logger *slog.Logger) *PoolBootstrapper {
	if logger == nil {
		logger = slog.Default()
	}
	return &PoolBootstrapper{accounts: accounts, workers: workers, log: logger}
}

// backfillBatch bounds one pass over unlocated accounts.
const backfillBatch = 500

// BackfillAccounts gives every location-less account a deterministic city, GPS
// point, timezone and user agent.
//
// Deterministic on the account id: two boots must produce the SAME city for the
// same account, or the stable device identity the design rests on would change
// on every restart. Hashing also spreads the fleet across cities instead of
// pinning everyone to whichever city sorts first.
func (b *PoolBootstrapper) BackfillAccounts(ctx context.Context) (int, error) {
	if b.accounts == nil {
		return 0, nil
	}
	pending, err := b.accounts.ListUnlocated(ctx, backfillBatch)
	if err != nil {
		return 0, fmt.Errorf("pool bootstrap: list unlocated: %w", err)
	}
	updated := 0
	for _, acc := range pending {
		if ctx.Err() != nil {
			return updated, ctx.Err()
		}
		city := domain.Cities[fnv32a(acc.ID)%uint32(len(domain.Cities))]
		seed := fnv32a(acc.ID)
		//nolint:gosec // deterministic on purpose: the same account must keep
		// the same coordinate across restarts, so a seeded stream is the tool.
		rnd := rand.New(rand.NewPCG(uint64(seed), uint64(seed)^0x9e3779b97f4a7c15))
		lat, lng := city.RandomPoint(rnd)

		acc.Location = new(city.Name)
		acc.Latitude = &lat
		acc.Longitude = &lng
		acc.Timezone = domain.TimezoneForCity(city.Name)
		if acc.UserAgent == nil {
			acc.UserAgent = new(domain.DefaultDesktopUserAgent)
		}
		if _, err := b.accounts.Update(ctx, acc); err != nil {
			return updated, fmt.Errorf("pool bootstrap: locate account %s: %w", acc.ID, err)
		}
		updated++
	}
	if updated > 0 {
		b.log.Info("pool bootstrap: located accounts", "count", updated)
	}
	return updated, nil
}

// EnsurePoolWorkers seeds the pool worker rows by name, so a fresh database (and
// every restart) has the ten candidates the coordinator shuffles through.
func (b *PoolBootstrapper) EnsurePoolWorkers(ctx context.Context, defs []PoolWorkerDef) error {
	if b.workers == nil {
		return nil
	}
	for _, def := range defs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		w := domain.Worker{
			Name:   def.Name,
			Region: def.Region,
			Source: domain.SourceManual,
			Status: domain.WorkerIdle,
		}
		if def.RPCURL != "" {
			w.EndpointURL = new(def.RPCURL)
		}
		if def.NoVNCURL != "" {
			w.NoVNCService = new(def.NoVNCURL)
		}
		if _, err := b.workers.UpsertPoolWorker(ctx, w); err != nil {
			return fmt.Errorf("pool bootstrap: seed worker %s: %w", def.Name, err)
		}
	}
	if len(defs) > 0 {
		b.log.Info("pool bootstrap: workers seeded", "count", len(defs))
	}
	return nil
}

// fnv32a is FNV-1a over the id: stable across processes and runs, unlike Go's
// per-process string hashing. The slot coordinator hashes the same way, so an
// account's city and its preferred machine come from one identity.
func fnv32a(id string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return h.Sum32()
}
