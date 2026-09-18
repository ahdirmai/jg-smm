package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
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
	control  port.Publisher
	imports  *importLimiter
	logger   *slog.Logger
}

// AccountConfig tunes the account service.
type AccountConfig struct {
	Sealer port.Sealer
	Clock  port.Clock
	Stream port.StreamPublisher
	// Control publishes auth-login/auth-input on a worker's control channel.
	// Optional: nil keeps account CRUD working, only the operator login flow is
	// unavailable.
	Control port.Publisher
	Logger  *slog.Logger
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
		control:  cfg.Control,
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
	// Release the slot after the status write: the row is paused either way, and
	// a failed release must not swallow the pause. An AUTO container left empty
	// by this is reaped, which is the point of pausing — the worker stops
	// presenting the account. Resume packs it back into a live slot.
	if s.packer != nil {
		if err := s.packer.Release(ctx, accountID); err != nil {
			s.logger.Warn("account paused but slot not released", "accountId", accountID, "err", err)
		}
	}
	s.logger.Info("account paused", "accountId", accountID)
	view := toAccountView(a)
	s.publishAccount(ctx, view)
	return view, nil
}

// Resume reactivates a paused account. If a packer is wired it is placed back
// into a container; resume does not require a fresh login because the session
// persisted on the container's PVC.
//
// Resuming a QUARANTINED account is the P5-02 manual reset: the operator has
// looked at it and decided it is safe, so the health score is restored and the
// account re-enters the pool. Without this, an auto-quarantined account could
// only be removed, not recovered.
func (s *AccountService) Resume(ctx context.Context, accountID string) (AccountSummary, error) {
	a, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return AccountSummary{}, fmt.Errorf("account service: get: %w", err)
	}
	if a.Status != domain.AccountPaused && a.Status != domain.AccountQuarantined {
		return toAccountView(a), nil
	}
	if a.Status == domain.AccountQuarantined {
		a.HealthScore = DefaultHealthScorePolicy.ScoreMax
		s.logger.Info("account reset from quarantine", "accountId", accountID, "score", a.HealthScore)
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

// Login dispatches an operator headful login (P1-11 / P1-12). The worker opens
// the platform login page in its noVNC-visible browser; the operator types the
// credential there, so the API never sees it. The row flips to PENDING_AUTH
// immediately — the card shows the live view while the login is in flight —
// and the worker reports the real outcome over /internal/account-callback.
func (s *AccountService) Login(ctx context.Context, accountID string) (AccountSummary, error) {
	if s.control == nil {
		return AccountSummary{}, fmt.Errorf("%w: auth control channel is not configured", domain.ErrUnavailable)
	}
	a, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return AccountSummary{}, fmt.Errorf("account service: get: %w", err)
	}
	if a.WorkerID == nil {
		return AccountSummary{}, fmt.Errorf("%w: account has no container to log in on", domain.ErrConflict)
	}
	w, err := s.workers.GetByID(ctx, *a.WorkerID)
	if err != nil {
		return AccountSummary{}, fmt.Errorf("account service: worker: %w", err)
	}
	if w.NoVNCService == nil {
		return AccountSummary{}, fmt.Errorf("%w: worker has no live view to log in through", domain.ErrConflict)
	}
	msg, err := json.Marshal(controlMessage{
		Type:      "auth-login",
		AccountID: accountID,
		Platform:  string(a.Platform),
	})
	if err != nil {
		return AccountSummary{}, fmt.Errorf("account service: marshal control: %w", err)
	}
	if err := s.control.PublishControl(ctx, w.ID, msg); err != nil {
		return AccountSummary{}, fmt.Errorf("account service: publish auth-login: %w", err)
	}
	// Optimistic state: the callback is the authority, but the operator should
	// not have to refresh to see the login started.
	a.AuthStatus = domain.AuthAuthenticating
	if _, err := s.accounts.Update(ctx, a); err != nil {
		return AccountSummary{}, fmt.Errorf("account service: mark pending auth: %w", err)
	}
	s.logger.Info("auth-login dispatched", "accountId", accountID, "workerId", w.ID)
	view := toAccountView(a)
	s.publishAccount(ctx, view)
	return view, nil
}

// SubmitInput carries a 2FA / checkpoint code to a parked login. Only valid
// while the worker still holds the context (authStatus PENDING_AUTH); a login
// that already settled is a conflict rather than a crash.
func (s *AccountService) SubmitInput(ctx context.Context, accountID, value string) (AccountSummary, error) {
	if s.control == nil {
		return AccountSummary{}, fmt.Errorf("%w: auth control channel is not configured", domain.ErrUnavailable)
	}
	a, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return AccountSummary{}, fmt.Errorf("account service: get: %w", err)
	}
	if a.WorkerID == nil {
		return AccountSummary{}, fmt.Errorf("%w: account has no container to log in on", domain.ErrConflict)
	}
	if a.AuthStatus != domain.AuthAuthenticating {
		return AccountSummary{}, fmt.Errorf("%w: account login is not awaiting input", domain.ErrConflict)
	}
	msg, err := json.Marshal(controlMessage{
		Type:      "auth-input",
		AccountID: accountID,
		Platform:  string(a.Platform),
		Payload:   map[string]string{"value": value},
	})
	if err != nil {
		return AccountSummary{}, fmt.Errorf("account service: marshal control: %w", err)
	}
	if err := s.control.PublishControl(ctx, *a.WorkerID, msg); err != nil {
		return AccountSummary{}, fmt.Errorf("account service: publish auth-input: %w", err)
	}
	s.logger.Info("auth-input dispatched", "accountId", accountID, "workerId", *a.WorkerID)
	return toAccountView(a), nil
}

// controlMessage is the wire shape the worker's control subscriber expects.
type controlMessage struct {
	Type      string            `json:"type"`
	AccountID string            `json:"accountId"`
	Platform  string            `json:"platform"`
	Payload   map[string]string `json:"payload,omitempty"`
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
//
// JSON tags match the REST account/container contract: this struct is
// marshaled straight onto the SSE stream, and the browser reconciles it
// against the API's Container/Account shape.
type AccountSummary struct {
	ID          string               `json:"id"`
	Platform    domain.Platform      `json:"platform"`
	Username    string               `json:"username"`
	Handle      *string              `json:"handle"`
	AuthStatus  domain.AuthStatus    `json:"authStatus"`
	Status      domain.AccountStatus `json:"status"`
	HealthScore int                  `json:"healthScore"`
	WorkerID    *string              `json:"workerId"`
	Tags        []string             `json:"tags"`
	LastError   *string              `json:"lastError"`
	CreatedAt   string               `json:"createdAt"`
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
