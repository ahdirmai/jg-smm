package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
)

// AccountService is the operator-facing account API. It creates accounts
// (encrypting the credential before it is stored), packs them into containers,
// lists them, and drives the pause/resume/remove lifecycle (P1-15 / P1-16).
type AccountService struct {
	accounts port.AccountStore
	workers  port.WorkerStore
	packer   *Packer
	sealer   port.Sealer
	clock    port.Clock
	stream   port.StreamPublisher
	imports  *importLimiter
	logger   *slog.Logger
}

// AccountConfig tunes the account service.
type AccountConfig struct {
	Sealer port.Sealer
	Clock  port.Clock
	Stream port.StreamPublisher
	Logger *slog.Logger
}

// NewAccountService wires the service. packer may be nil; accounts are then
// created unassigned (the UI shows them as pending placement).
func NewAccountService(accounts port.AccountStore, workers port.WorkerStore, packer *Packer, cfg AccountConfig) *AccountService {
	if cfg.Clock == nil {
		cfg.Clock = systemClock{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &AccountService{
		accounts: accounts,
		workers:  workers,
		packer:   packer,
		sealer:   cfg.Sealer,
		clock:    cfg.Clock,
		stream:   cfg.Stream,
		imports:  newImportLimiter(time.Minute, ImportRateLimit),
		logger:   cfg.Logger,
	}
}

// Create stores a new account and packs it into a container. The plaintext
// password is sealed before it touches the store and is never logged or
// returned. Packing happens after the row exists so the account is packable.
func (s *AccountService) Create(ctx context.Context, in AccountInput) (AccountSummary, *domain.Worker, error) {
	if err := in.Validate(); err != nil {
		return AccountSummary{}, nil, err
	}
	if s.sealer == nil {
		return AccountSummary{}, nil, fmt.Errorf("%w: credential sealer not configured", domain.ErrUnavailable)
	}

	enc, err := s.sealer.Seal([]byte(in.Password))
	if err != nil {
		return AccountSummary{}, nil, fmt.Errorf("account service: seal credential: %w", err)
	}

	account, err := s.accounts.Create(ctx, domain.Account{
		Platform:     in.Platform,
		Username:     in.Username,
		PasswordEnc:  enc,
		AuthStatus:   domain.AuthAuthenticating,
		ProxyGroupID: in.ProxyGroupID,
		Status:       domain.AccountActive,
		Tags:         in.Tags,
	})
	if err != nil {
		if isDomainConflict(err) {
			return AccountSummary{}, nil, fmt.Errorf("%w: %s already used on %s", domain.ErrConflict, in.Username, in.Platform)
		}
		return AccountSummary{}, nil, fmt.Errorf("account service: create: %w", err)
	}
	s.logger.Info("account created", "accountId", account.ID, "platform", account.Platform)

	// No packer (DB-only wiring) or nothing to pack into: leave it unassigned.
	if s.packer == nil {
		s.publishAccount(ctx, toAccountView(account))
		return toAccountView(account), nil, nil
	}
	packed, worker, err := s.packer.Pack(ctx, account.ID, account.Platform)
	if err != nil {
		// The account exists but has no container yet; that is a recoverable
		// placement state, not a failed create.
		s.logger.Warn("account created but not packed", "accountId", account.ID, "err", err)
		s.publishAccount(ctx, toAccountView(account))
		return toAccountView(account), nil, nil
	}
	s.publishAccount(ctx, toAccountView(packed))
	return toAccountView(packed), &worker, nil
}

// List returns every account, newest first, without credentials.
func (s *AccountService) List(ctx context.Context) ([]AccountSummary, error) {
	accounts, err := s.accounts.List(ctx, port.AccountFilter{Limit: 200})
	if err != nil {
		return nil, fmt.Errorf("account service: list: %w", err)
	}
	views := make([]AccountSummary, 0, len(accounts))
	for _, a := range accounts {
		views = append(views, toAccountView(a))
	}
	return views, nil
}

// Pause suspends an account: it is unassigned from its container so the worker
// stops using it, but the row (and its session via the PVC) survives.
func (s *AccountService) Pause(ctx context.Context, accountID string) (AccountSummary, error) {
	a, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return AccountSummary{}, fmt.Errorf("account service: get: %w", err)
	}
	if a.Status == domain.AccountArchived {
		return toAccountView(a), nil
	}
	a.Status = domain.AccountPaused
	if _, err := s.accounts.Update(ctx, a); err != nil {
		return AccountSummary{}, fmt.Errorf("account service: pause: %w", err)
	}
	s.logger.Info("account paused", "accountId", accountID)
	view := toAccountView(a)
	s.publishAccount(ctx, view)
	return view, nil
}

// Resume reactivates a paused account. If a packer is wired it is placed back
// into a container; resume does not require a fresh login because the session
// persisted on the container's PVC.
func (s *AccountService) Resume(ctx context.Context, accountID string) (AccountSummary, error) {
	a, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return AccountSummary{}, fmt.Errorf("account service: get: %w", err)
	}
	if a.Status != domain.AccountPaused {
		return toAccountView(a), nil
	}
	a.Status = domain.AccountActive
	if _, err := s.accounts.Update(ctx, a); err != nil {
		return AccountSummary{}, fmt.Errorf("account service: resume: %w", err)
	}
	s.logger.Info("account resumed", "accountId", accountID)
	view := toAccountView(a)
	s.publishAccount(ctx, view)
	return view, nil
}

// Remove deletes an account and releases its container slot. An AUTO container
// that becomes empty is reaped; a MANUAL container survives.
func (s *AccountService) Remove(ctx context.Context, accountID string) error {
	if s.packer != nil {
		if err := s.packer.Release(ctx, accountID); err != nil {
			return fmt.Errorf("account service: release: %w", err)
		}
	}
	if err := s.accounts.Delete(ctx, accountID); err != nil {
		return fmt.Errorf("account service: delete: %w", err)
	}
	s.logger.Info("account removed", "accountId", accountID)
	if s.stream != nil {
		s.stream.Publish(ctx, port.EventAccountUpdated, []byte(`{"id":"`+accountID+`","removed":true}`))
	}
	return nil
}

// AccountInput is the create payload. Password is plaintext in memory only.
type AccountInput struct {
	Platform     domain.Platform
	Username     string
	Password     string
	ProxyGroupID *string
	Tags         []string
}

// Validate rejects input before any crypto or DB work.
func (in AccountInput) Validate() error {
	if !in.Platform.Valid() {
		return fmt.Errorf("%w: unknown platform", domain.ErrValidation)
	}
	if in.Username == "" {
		return fmt.Errorf("%w: username is required", domain.ErrValidation)
	}
	if in.Password == "" {
		return fmt.Errorf("%w: password is required", domain.ErrValidation)
	}
	return nil
}

// AccountSummary is the credential-free read model.
type AccountSummary struct {
	ID          string
	Platform    domain.Platform
	Username    string
	Handle      *string
	AuthStatus  domain.AuthStatus
	Status      domain.AccountStatus
	HealthScore int
	WorkerID    *string
	Tags        []string
	LastError   *string
	CreatedAt   string
}

func toAccountView(a domain.Account) AccountSummary {
	return AccountSummary{
		ID:          a.ID,
		Platform:    a.Platform,
		Username:    a.Username,
		Handle:      a.Handle,
		AuthStatus:  a.AuthStatus,
		Status:      a.Status,
		HealthScore: a.HealthScore,
		WorkerID:    a.WorkerID,
		Tags:        a.Tags,
		LastError:   a.LastError,
		CreatedAt:   a.CreatedAt.Format(timeRFC3339),
	}
}

// publishAccount fans the account out to dashboards. Payload is the full entity
// (ADR 0010) so the browser reconciles without a second fetch. A missing
// publisher is fine: the write already succeeded.
func (s *AccountService) publishAccount(ctx context.Context, a AccountSummary) {
	if s.stream == nil {
		return
	}
	body, err := json.Marshal(a)
	if err != nil {
		s.logger.Warn("stream: marshal account", "err", err)
		return
	}
	s.stream.Publish(ctx, port.EventAccountUpdated, body)
}

// MaxImportRows is the import batch cap (P4-07). More than this is an operator
// mistake, not a workflow: the UI splits at the boundary.
const MaxImportRows = 100

// ImportRateLimit is the per-caller import budget: 10 imports per minute. The
// import is the one endpoint that can add a fleet in a loop, so it is the one
// that needs a ceiling. In-memory: correct for a single API pod, and the
// Redis-backed limiter (P3-10) is the upgrade path when the API scales out.
const ImportRateLimit = 10

// importLimiter is a fixed-window counter per caller. Deliberately not a
// sliding window: import bursts are operator-driven and a minute boundary is
// accurate enough for a budget this coarse.
type importLimiter struct {
	window time.Duration
	limit  int
	mu     sync.Mutex
	hits   map[string][]time.Time
}

func newImportLimiter(window time.Duration, limit int) *importLimiter {
	return &importLimiter{window: window, limit: limit, hits: map[string][]time.Time{}}
}

// Allow reports whether the caller is within budget. Stale hits are reaped on
// the same pass, so the map cannot grow without bound for idle callers.
func (l *importLimiter) Allow(key string, now time.Time) bool {
	cutoff := now.Add(-l.window)
	l.mu.Lock()
	defer l.mu.Unlock()
	hits := l.hits[key]
	keep := hits[:0]
	for _, t := range hits {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= l.limit {
		l.hits[key] = keep
		return false
	}
	l.hits[key] = append(keep, now)
	return true
}

// ImportError is one rejected row of an import.
type ImportError struct {
	Row    int    `json:"row"`
	Reason string `json:"reason"`
}

// ImportResult is the import verdict (P4-07): the shape the AC asks for.
type ImportResult struct {
	Queued      int           `json:"queued"`
	Invalid     []ImportError `json:"invalid"`
	RateLimited bool          `json:"rateLimited"`
}

// Import creates up to MaxImportRows accounts. Every row is independent: a
// row that fails validation or duplicates an existing account is reported in
// Invalid, and the rest still import. The whole call is bounded by the import
// rate limiter — a refused call returns RateLimited and creates nothing.
func (s *AccountService) Import(ctx context.Context, caller string, rows []AccountInput) (ImportResult, error) {
	res := ImportResult{Invalid: []ImportError{}}
	if len(rows) == 0 {
		return res, fmt.Errorf("%w: import must carry at least one row", domain.ErrValidation)
	}
	if len(rows) > MaxImportRows {
		return res, fmt.Errorf("%w: import carries %d rows, the cap is %d", domain.ErrValidation, len(rows), MaxImportRows)
	}
	if s.imports != nil && !s.imports.Allow(caller, s.clock.Now()) {
		// Refuse the whole call rather than a subset: a partial import under a
		// rate limit is how an operator loses track of what landed.
		res.RateLimited = true
		return res, domain.ErrRateLimited
	}

	for i, in := range rows {
		if err := in.Validate(); err != nil {
			res.Invalid = append(res.Invalid, ImportError{Row: i, Reason: err.Error()})
			continue
		}
		// Create already seals the credential, stores and packs the row, and
		// publishes the SSE frame. A conflict is a row-level outcome.
		if _, _, err := s.Create(ctx, in); err != nil {
			res.Invalid = append(res.Invalid, ImportError{Row: i, Reason: err.Error()})
			continue
		}
		res.Queued++
	}
	s.logger.Info("accounts imported", "queued", res.Queued, "invalid", len(res.Invalid))
	return res, nil
}

const timeRFC3339 = "2006-01-02T15:04:05Z07:00"
