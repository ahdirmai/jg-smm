package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
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
	// sessions keeps one encrypted browser session per account so a rotating
	// container can pick an account up without a fresh login (migration 000020).
	// Optional: nil keeps CRUD working but every container swap needs a login.
	sessions port.BrowserSessionStore
	// waker resumes a PAUSED container before a login is dispatched. Optional:
	// nil means containers are assumed to be running (the pre-pause behaviour).
	waker *Waker
	// poolMode selects placement: "stateless" leaves accounts unassigned (a
	// worker is leased per job), anything else keeps the container packer.
	poolMode string
	// pool is the stateless fleet, used by StartLogin/LiveView to lease a worker
	// and push the context. Optional: nil keeps the control-channel path.
	pool *PoolDispatch
	// novncTicketTTL is how long a live-view URL stays usable.
	novncTicketTTL time.Duration
	// novncSecret signs the live-view ticket. Empty disables live view rather
	// than minting URLs nobody can verify.
	novncSecret string

	// exports correlates an in-flight session export with the worker callback
	// that fulfils it. In-memory and short-lived: a plaintext session is never
	// persisted server-side, it is held only long enough to hand to the caller.
	exportMu sync.Mutex
	exports  map[string]chan json.RawMessage
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
	// Sessions persists the per-account cookie so a container swap does not
	// force a re-login. Optional: nil falls back to login-per-container.
	Sessions port.BrowserSessionStore
	// Waker resumes a paused container before a login. Optional: nil assumes it
	// is already running.
	Waker  *Waker
	Logger *slog.Logger
	// PoolMode is config.WorkerPoolMode. "stateless" disables packing.
	PoolMode string
	// Pool is the stateless fleet for logins and live view. Optional.
	Pool *PoolDispatch
	// NoVNCSecret signs live-view tickets; NoVNCTicketTTL bounds their life.
	NoVNCSecret    string
	NoVNCTicketTTL time.Duration
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
		accounts:       accounts,
		workers:        workers,
		packer:         packer,
		sealer:         cfg.Sealer,
		clock:          cfg.Clock,
		stream:         cfg.Stream,
		control:        cfg.Control,
		imports:        newImportLimiter(time.Minute, ImportRateLimit),
		sessions:       cfg.Sessions,
		waker:          cfg.Waker,
		logger:         cfg.Logger,
		poolMode:       cfg.PoolMode,
		pool:           cfg.Pool,
		novncSecret:    cfg.NoVNCSecret,
		novncTicketTTL: cfg.NoVNCTicketTTL,
		exports:        map[string]chan json.RawMessage{},
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

	// The device identity is decided BEFORE the insert so a new account is
	// never briefly unlocated (the column triple is all-or-nothing).
	loc, err := resolveLocation(in.Location, in.Username+string(in.Platform))
	if err != nil {
		return AccountSummary{}, nil, err
	}
	account, err := s.accounts.Create(ctx, domain.Account{
		Platform:     in.Platform,
		Username:     in.Username,
		PasswordEnc:  enc,
		AuthStatus:   domain.AuthAuthenticating,
		ProxyGroupID: in.ProxyGroupID,
		Status:       domain.AccountActive,
		Tags:         in.Tags,
		Location:     loc.location,
		Latitude:     loc.latitude,
		Longitude:    loc.longitude,
		Timezone:     loc.timezone,
		UserAgent:    new(domain.DefaultDesktopUserAgent),
	})
	if err != nil {
		if isDomainConflict(err) {
			return AccountSummary{}, nil, fmt.Errorf("%w: %s already used on %s", domain.ErrConflict, in.Username, in.Platform)
		}
		return AccountSummary{}, nil, fmt.Errorf("account service: create: %w", err)
	}
	s.logger.Info("account created", "accountId", account.ID, "platform", account.Platform)

	// Pool mode has no per-account container to pack into: the account is
	// schedulable the moment the row exists, and a worker is leased per job.
	if s.poolMode == "stateless" {
		s.publishAccount(ctx, toAccountView(account))
		return toAccountView(account), nil, nil
	}
	// No packer (DB-only wiring) or nothing to pack into: leave it unassigned.
	if s.packer == nil {
		if in.WorkerID != nil {
			return AccountSummary{}, nil, fmt.Errorf("%w: container pick requested but packer is not configured", domain.ErrUnavailable)
		}
		s.publishAccount(ctx, toAccountView(account))
		return toAccountView(account), nil, nil
	}
	// An operator-picked container is validated BEFORE the row is written: a
	// bad pick fails the request outright, never a half-created account to
	// roll back. PackInto re-checks; UNIQUE(worker_id, platform) is the
	// authority under concurrency.
	if in.WorkerID != nil {
		if _, cerr := s.packer.ResolveContainer(ctx, *in.WorkerID, in.Platform); cerr != nil {
			return AccountSummary{}, nil, cerr
		}
		packed, worker, perr := s.packer.PackInto(ctx, account.ID, account.Platform, *in.WorkerID)
		if perr != nil {
			s.logger.Warn("account created but picked container rejected", "accountId", account.ID, "workerId", *in.WorkerID, "err", perr)
			s.publishAccount(ctx, toAccountView(account))
			return toAccountView(account), nil, nil
		}
		s.publishAccount(ctx, toAccountView(packed))
		return toAccountView(packed), &worker, nil
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
	const pageSize = 200
	views := make([]AccountSummary, 0)
	for offset := 0; ; offset += pageSize {
		accounts, err := s.accounts.List(ctx, port.AccountFilter{Limit: pageSize, Offset: offset})
		if err != nil {
			return nil, fmt.Errorf("account service: list: %w", err)
		}
		for _, a := range accounts {
			views = append(views, toAccountView(a))
		}
		if len(accounts) < pageSize {
			return views, nil
		}
	}
}

// RegionGroup is the accounts operating from one region ("wilayah"), the unit
// the region-select comment flow fans an action out over.
type RegionGroup struct {
	Region   string           `json:"region"`
	Accounts []AccountSummary `json:"accounts"`
}

// UnassignedRegion labels accounts whose worker has no region, or that have no
// worker at all. It sorts last so a real region is never hidden behind it.
const UnassignedRegion = "unassigned"

// AccountsByRegion groups every account by its OWN city (account.location).
//
// It used to join through worker.location, which no longer means anything: a
// pool worker is shared and disposable, so the machine says nothing about where
// an account is. It also read a single 200-row page, which silently truncated a
// 486-account fleet; ListAll is the honest read.
func (s *AccountService) AccountsByRegion(ctx context.Context) ([]RegionGroup, error) {
	accounts, err := s.accounts.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("account service: by-region: list accounts: %w", err)
	}

	byRegion := map[string][]AccountSummary{}
	for _, a := range accounts {
		region := UnassignedRegion
		if a.Location != nil && strings.TrimSpace(*a.Location) != "" {
			region = strings.TrimSpace(*a.Location)
		}
		byRegion[region] = append(byRegion[region], toAccountView(a))
	}

	regions := make([]string, 0, len(byRegion))
	for r := range byRegion {
		regions = append(regions, r)
	}
	// Real regions alphabetical, UnassignedRegion always last.
	sort.Slice(regions, func(i, j int) bool {
		if regions[i] == UnassignedRegion {
			return false
		}
		if regions[j] == UnassignedRegion {
			return true
		}
		return regions[i] < regions[j]
	})

	groups := make([]RegionGroup, 0, len(regions))
	for _, r := range regions {
		groups = append(groups, RegionGroup{Region: r, Accounts: byRegion[r]})
	}
	return groups, nil
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
		// Clear the refresh-failure streak too (P2-06). Without this the count
		// is still at the threshold, so the next refresh failure — one, not
		// three — would re-quarantine the account the operator just released.
		a.RefreshFailures = 0
		s.logger.Info("account reset from quarantine",
			"accountId", accountID, "score", a.HealthScore)
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
	// Dispatch on the MODE, not on the pointer: poolMode is what the operator
	// configured, and a pool without a dispatcher must fail loudly inside the
	// pool path rather than silently falling back to the container channel (and
	// skipping the lease guards that path does not have).
	if s.poolMode == "stateless" && s.pool != nil {
		return s.loginPooled(ctx, accountID)
	}
	if s.control == nil {
		return AccountSummary{}, fmt.Errorf("%w: auth control channel is not configured", domain.ErrUnavailable)
	}
	a, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return AccountSummary{}, fmt.Errorf("account service: get: %w", err)
	}
	// The credential blob is write-only in the read model: fetch it once, here,
	// for the dispatch only. The unsealed value never enters the row, a log,
	// or a response — it crosses exactly one private wire hop (the control
	// channel) and lives in the worker's memory for the login only.
	enc, err := s.accounts.GetPasswordEnc(ctx, accountID)
	if err != nil {
		return AccountSummary{}, fmt.Errorf("account service: get credential: %w", err)
	}
	if a.WorkerID == nil {
		return AccountSummary{}, fmt.Errorf("%w: account has no container to log in on", domain.ErrConflict)
	}
	w, err := s.workers.GetByID(ctx, *a.WorkerID)
	if err != nil {
		return AccountSummary{}, fmt.Errorf("account service: worker: %w", err)
	}
	// A login is a human-facing flow: the container has to be running before
	// the dispatch lands, or the control message sits in a queue nothing is
	// draining and the operator stares at a dead live view. Resume it first.
	if s.waker != nil {
		if err := s.waker.Wake(ctx, w.ID); err != nil {
			return AccountSummary{}, fmt.Errorf("%w: could not start the container for login: %v", domain.ErrUnavailable, err)
		}
	}
	// A legacy paused worker publishes its live URL only after provisioning.
	if w.NoVNCService == nil || strings.TrimSpace(*w.NoVNCService) == "" {
		w, err = s.workers.GetByID(ctx, w.ID)
		if err != nil {
			return AccountSummary{}, fmt.Errorf("account service: refresh worker: %w", err)
		}
		if w.NoVNCService == nil || strings.TrimSpace(*w.NoVNCService) == "" {
			return AccountSummary{}, fmt.Errorf("%w: worker has no live view to log in through", domain.ErrConflict)
		}
	}
	// Hand the worker the stored session FIRST when we have one. The worker
	// hydrates its context from `auth-import` and then probes: a live cookie
	// reports `verified` on the first poll, so the login never reaches the
	// form. That is what makes a rotating container viable — the account is
	// picked up by a different container without paying for another login.
	// When the cookie is dead the login proceeds exactly as before, so this is
	// an optimisation that cannot make a healthy login worse.
	if err := s.pushStoredSession(ctx, a, w.ID); err != nil {
		// Not fatal: a missing/unreadable session is the normal state of a
		// fresh account, and an import failure just means a real login runs.
		s.logger.Warn("account service: session restore skipped", "accountId", accountID, "err", err)
	}

	payload := authLoginPayload(s.sealer, a, enc, s.logger)
	msg, err := json.Marshal(controlMessage{
		Type:      "auth-login",
		AccountID: accountID,
		Platform:  string(a.Platform),
		Payload:   payload,
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
	// Stamp the dispatch time. This is what the stale-login sweep measures
	// from: without it an operator login whose callback never arrived left the
	// row in AUTHENTICATING with a null timestamp, which the sweep skips (it
	// must, or it would reclaim the 41 never-logged-in accounts created with
	// AUTHENTICATING as their birth state) — so the row would be stranded
	// permanently with nothing able to tell the two cases apart.
	dispatchedAt := s.clock.Now()
	a.LastChecked = &dispatchedAt
	if _, err := s.accounts.Update(ctx, a); err != nil {
		return AccountSummary{}, fmt.Errorf("account service: mark pending auth: %w", err)
	}
	s.logger.Info("auth-login dispatched", "accountId", accountID, "workerId", w.ID)
	view := toAccountView(a)
	s.publishAccount(ctx, view)
	return view, nil
}

// pushStoredSession hands a worker the account's saved cookie, if there is one.
//
// Returns nil (not an error) when no session is stored — that is the normal
// state of an account that has never logged in. The session is unsealed here
// and crosses exactly one private wire hop, like the password does.
func (s *AccountService) pushStoredSession(ctx context.Context, a domain.Account, workerID string) error {
	if s.sessions == nil || s.sealer == nil || s.control == nil {
		return nil
	}
	sealed, err := s.sessions.GetState(ctx, a.ID)
	if err != nil {
		return fmt.Errorf("read stored session: %w", err)
	}
	if len(sealed) == 0 {
		return nil
	}
	plain, err := s.sealer.Open(sealed)
	if err != nil {
		return fmt.Errorf("unseal stored session: %w", err)
	}
	var ctrl sessionImportControl
	ctrl.Type = "auth-import"
	ctrl.AccountID = a.ID
	ctrl.Platform = string(a.Platform)
	ctrl.Payload.Session = json.RawMessage(plain)
	msg, err := json.Marshal(ctrl)
	if err != nil {
		return fmt.Errorf("marshal auth-import: %w", err)
	}
	if err := s.control.PublishControl(ctx, workerID, msg); err != nil {
		return fmt.Errorf("publish auth-import: %w", err)
	}
	// Record the reuse: distinguishes a cookie that has actually carried an
	// account across containers from one that was captured and never tried.
	if err := s.sessions.Touch(ctx, a.ID); err != nil {
		s.logger.Warn("account service: touch session failed", "accountId", a.ID, "err", err)
	}
	return nil
}

// SubmitInput carries a 2FA / checkpoint code to a parked login. Valid while
// the worker still holds the parked context: the login was started
// (AUTHENTICATING) or the worker parked at a 2FA/checkpoint field and reported
// NEEDS_INPUT — the state the UI shows the "Submit code" button on. A login
// that already settled (AUTHENTICATED / FAILED) is a conflict, not a crash.
func (s *AccountService) SubmitInput(ctx context.Context, accountID, value string) (AccountSummary, error) {
	if s.pool != nil {
		return s.submitInputPooled(ctx, accountID, value)
	}
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
	if a.AuthStatus != domain.AuthAuthenticating && a.AuthStatus != domain.AuthNeedsInput {
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
	Type      string `json:"type"`
	AccountID string `json:"accountId"`
	Platform  string `json:"platform"`
	// Payload carries dispatch-specific data (auth-login: the unsealed
	// credentials; auth-input: the 2FA code; auth-export: the requestId).
	Payload map[string]string `json:"payload,omitempty"`
}

// authLoginPayload unseals the account's stored credentials for a single
// auth-login dispatch. The plaintext is carried on the private per-worker
// control channel (the same one that carries 2FA codes via auth-input) and
// lives in the worker's memory for the login only; neither the API log nor the
// control log line ever includes it. Nil sealer or blob means an empty payload
// and the operator types in the live view, exactly as before.
func authLoginPayload(sealer port.Sealer, a domain.Account, enc []byte, log *slog.Logger) map[string]string {
	if sealer == nil || len(enc) == 0 {
		return nil
	}
	pw, err := sealer.Open(enc)
	if err != nil {
		log.Warn("auth-login: credential unseal failed, operator types manually",
			"accountId", a.ID, "err", err)
		return nil
	}
	return map[string]string{"username": a.Username, "password": string(pw)}
}

// sessionImportControl carries a session (cookies) as a raw JSON object to the
// worker. It is separate from controlMessage because the payload here is a JSON
// object, not the string map the auth flow uses.
type sessionImportControl struct {
	Type      string `json:"type"`
	AccountID string `json:"accountId"`
	Platform  string `json:"platform"`
	Payload   struct {
		Session json.RawMessage `json:"session"`
	} `json:"payload"`
}

// sessionExportTimeout bounds how long an export request waits for the worker
// to dump the session. Generous enough for a busy control channel, short enough
// that the operator is not left hanging.
const sessionExportTimeout = 20 * time.Second

// ExportSession asks the account's worker to dump its persisted session
// (cookies) and returns that JSON to the caller ONCE. SECURITY: the session is
// a credential — it is never persisted server-side, only relayed in-memory to
// the caller, and never logged. Owner/admin-only is enforced at the route.
func (s *AccountService) ExportSession(ctx context.Context, accountID string) (json.RawMessage, error) {
	// Pool mode: the session IS the database row. An idle account has no worker
	// to ask (worker_id nil is its normal state), and there is nothing on any
	// machine that the stored cookie does not already hold.
	if s.poolMode == "stateless" {
		return s.exportStoredSession(ctx, accountID)
	}
	if s.control == nil {
		return nil, fmt.Errorf("%w: auth control channel is not configured", domain.ErrUnavailable)
	}
	a, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("account service: get: %w", err)
	}
	if a.WorkerID == nil {
		return nil, fmt.Errorf("%w: account has no container to export a session from", domain.ErrConflict)
	}

	requestID, err := newRequestID()
	if err != nil {
		return nil, fmt.Errorf("account service: request id: %w", err)
	}
	ch := make(chan json.RawMessage, 1)
	s.registerExport(requestID, ch)
	defer s.unregisterExport(requestID)

	msg, err := json.Marshal(controlMessage{
		Type:      "auth-export",
		AccountID: accountID,
		Platform:  string(a.Platform),
		Payload:   map[string]string{"requestId": requestID},
	})
	if err != nil {
		return nil, fmt.Errorf("account service: marshal control: %w", err)
	}
	if err := s.control.PublishControl(ctx, *a.WorkerID, msg); err != nil {
		return nil, fmt.Errorf("account service: publish auth-export: %w", err)
	}
	// Deliberately no session value in this log line.
	s.logger.Info("auth-export dispatched", "accountId", accountID, "workerId", *a.WorkerID)

	select {
	case session := <-ch:
		if len(session) == 0 {
			return nil, fmt.Errorf("%w: no session is persisted for this account", domain.ErrNotFound)
		}
		// Persist on the way OUT, not only when the operator asks: the export
		// is the one moment the plaintext cookie is already in hand, and a
		// rotating fleet needs it stored to avoid a re-login on the next
		// container. Best-effort — a store failure must not deny the operator
		// the session they asked for, since they still hold it client-side.
		if s.sessions != nil && s.sealer != nil {
			if sealed, err := s.sealer.Seal(session); err != nil {
				s.logger.Warn("account service: seal session failed", "accountId", accountID, "err", err)
			} else if err := s.sessions.Save(ctx, accountID, sealed, a.CookieExpiryAt, a.WorkerID); err != nil {
				s.logger.Warn("account service: store session failed", "accountId", accountID, "err", err)
			}
		}
		return session, nil
	case <-time.After(sessionExportTimeout):
		return nil, fmt.Errorf("%w: worker did not return a session in time", domain.ErrUnavailable)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ImportSession hands a session (cookies) to the account's worker so a fresh
// container adopts it without a new login. The session is validated as a JSON
// object and relayed; it is never persisted or logged here.
func (s *AccountService) ImportSession(ctx context.Context, accountID string, session json.RawMessage) error {
	// Pool mode stores the cookie directly: the next dispatch ships it to
	// whichever worker runs the account, so there is no container to hand it to.
	if s.poolMode == "stateless" {
		return s.importStoredSession(ctx, accountID, session)
	}
	if s.control == nil {
		return fmt.Errorf("%w: auth control channel is not configured", domain.ErrUnavailable)
	}
	// Defensive validation: the payload must be a JSON object (a storageState),
	// not an array, scalar, or garbage.
	var probe map[string]json.RawMessage
	if len(session) == 0 || json.Unmarshal(session, &probe) != nil {
		return fmt.Errorf("%w: session must be a JSON object", domain.ErrValidation)
	}
	a, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return fmt.Errorf("account service: get: %w", err)
	}
	if a.WorkerID == nil {
		return fmt.Errorf("%w: account has no container to import a session into", domain.ErrConflict)
	}

	var ctrl sessionImportControl
	ctrl.Type = "auth-import"
	ctrl.AccountID = accountID
	ctrl.Platform = string(a.Platform)
	ctrl.Payload.Session = session
	msg, err := json.Marshal(ctrl)
	if err != nil {
		return fmt.Errorf("account service: marshal control: %w", err)
	}
	if err := s.control.PublishControl(ctx, *a.WorkerID, msg); err != nil {
		return fmt.Errorf("account service: publish auth-import: %w", err)
	}
	s.logger.Info("auth-import dispatched", "accountId", accountID, "workerId", *a.WorkerID)
	return nil
}

// DeliverExportedSession fulfils a waiting ExportSession with the session the
// worker dumped. A nil session (no file on the container) is delivered as an
// empty payload so the waiter can report "no session" rather than block. Called
// by the internal callback handler; unknown request ids are dropped (the export
// already timed out).
func (s *AccountService) DeliverExportedSession(requestID string, session json.RawMessage) {
	s.exportMu.Lock()
	ch := s.exports[requestID]
	s.exportMu.Unlock()
	if ch == nil {
		return
	}
	// Non-blocking: the channel is buffered for exactly one delivery, and a
	// duplicate callback must not block the internal handler.
	select {
	case ch <- session:
	default:
	}
}

// SessionPersistRequestID is the requestId a worker uses when it reports the
// session of a login it just completed. It is not an operator export: nothing
// is waiting on the channel, so the callback handler stores the session instead
// of relaying it. Fixed rather than per-request because the worker has no
// request to correlate with — the login was its own.
const SessionPersistRequestID = "login-success"

// PersistSession stores a session the worker reported on a successful login.
// This is the write that makes AUTHENTICATED mean something: before it, the
// cookie lived only on the container volume, so recreating the container (the
// fleet's normal PAUSED/RUNNING lifecycle) silently destroyed the proof and the
// row kept claiming a session that no longer existed.
//
// Best-effort by contract: the callback handler translates the error, but the
// login itself already succeeded and must not be reported as failed for it.
func (s *AccountService) PersistSession(ctx context.Context, accountID string, session json.RawMessage) error {
	if s.sessions == nil || s.sealer == nil {
		return fmt.Errorf("%w: session store is not configured", domain.ErrUnavailable)
	}
	if len(session) == 0 {
		return nil
	}
	a, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return fmt.Errorf("account service: get: %w", err)
	}
	sealed, err := s.sealer.Seal(session)
	if err != nil {
		return fmt.Errorf("account service: seal session: %w", err)
	}
	if err := s.sessions.Save(ctx, accountID, sealed, a.CookieExpiryAt, a.WorkerID); err != nil {
		return fmt.Errorf("account service: store session: %w", err)
	}
	s.logger.Info("login session persisted", "accountId", accountID, "workerId", a.WorkerID)
	return nil
}

func (s *AccountService) registerExport(requestID string, ch chan json.RawMessage) {
	s.exportMu.Lock()
	s.exports[requestID] = ch
	s.exportMu.Unlock()
}

func (s *AccountService) unregisterExport(requestID string) {
	s.exportMu.Lock()
	delete(s.exports, requestID)
	s.exportMu.Unlock()
}

// newRequestID returns a random correlation id for a session export.
func newRequestID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
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
	Platform domain.Platform
	Username string
	Password string
	// Location is a city name from domain.Cities. Empty lets the id pick one,
	// which spreads a 100-row import across cities instead of one.
	Location     *string
	ProxyGroupID *string
	// WorkerID pins the container to pack into instead of letting the packer
	// walk the fleet. Nil keeps the auto-pack behaviour.
	WorkerID *string
	Tags     []string
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
	Location    *string              `json:"location"`
	Latitude    *float64             `json:"latitude"`
	Longitude   *float64             `json:"longitude"`
	Timezone    string               `json:"timezone"`
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
		Location:    a.Location,
		Latitude:    a.Latitude,
		Longitude:   a.Longitude,
		Timezone:    a.Timezone,
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

// loginPooled starts a login on a leased pool worker.
//
// Same contract as Login (the account becomes AUTHENTICATING, the callback is the
// authority), different transport: there is no per-account container and no
// control channel a sleeping VM would miss, so the worker is leased and the
// login pushed over HTTPS. The lease is held until the outcome callback releases
// it — the page must stay open for the operator's 2FA.
func (s *AccountService) loginPooled(ctx context.Context, accountID string) (AccountSummary, error) {
	acc, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return AccountSummary{}, fmt.Errorf("account service: get: %w", err)
	}
	// A parked login is protected ONLY while it actually holds a worker. The
	// status alone is not evidence: a restart, an OTP skip, or a worker that
	// went to sleep leave the row NEEDS_INPUT with no lease, and nothing is
	// holding the operator's prompt any more. Refusing then stranded 26
	// accounts behind an error that told them to submit a code to a browser
	// that no longer exists.
	// A parked login blocks a new one ONLY while its browser is still alive.
	// The status plus a lease is not proof: the worker may have slept, been
	// rebuilt, or lost the context. Verify instead of guessing, and release the
	// stale session so the operator can simply press Login again — the old
	// message ("submit the code instead") pointed at a browser that might not
	// exist, and there was no other way out.
	// A lease is only evidence of a LIVE session while its browser is still
	// there. A row that says AUTHENTICATED or FAILED while still holding
	// worker_id is the leftover of a settled login whose release never landed
	// (a callback that timed out, an API restart between the write and the
	// release): the display is free, nothing is showing, and trying to lease
	// any machine fails because this account already occupies its own row.
	// Clear it and let the login proceed instead of refusing forever.
	if acc.WorkerID != nil && acc.AuthStatus != domain.AuthNeedsInput {
		live := s.parkedLoginIsLive(ctx, acc)
		if !live {
			s.logger.Info("stale lease cleared before login",
				"accountId", accountID, "workerId", *acc.WorkerID, "authStatus", acc.AuthStatus)
			if err := s.CloseLiveView(ctx, accountID); err != nil {
				return AccountSummary{}, fmt.Errorf("account service: clear stale lease: %w", err)
			}
			refreshed, err := s.accounts.GetByID(ctx, accountID)
			if err != nil {
				return AccountSummary{}, fmt.Errorf("account service: get: %w", err)
			}
			acc = refreshed
		}
	}
	if acc.AuthStatus == domain.AuthNeedsInput && acc.WorkerID != nil {
		if !s.parkedLoginIsLive(ctx, acc) {
			s.logger.Info("stale parked login cleared before retry", "accountId", accountID, "workerId", *acc.WorkerID)
			if err := s.CloseLiveView(ctx, accountID); err != nil {
				return AccountSummary{}, fmt.Errorf("account service: clear stale login: %w", err)
			}
			refreshed, err := s.accounts.GetByID(ctx, accountID)
			if err != nil {
				return AccountSummary{}, fmt.Errorf("account service: get: %w", err)
			}
			acc = refreshed
		} else {
			return AccountSummary{}, fmt.Errorf("%w: this login is waiting for operator input; submit the code instead", domain.ErrConflict)
		}
	}
	enc, err := s.accounts.GetPasswordEnc(ctx, accountID)
	if err != nil {
		return AccountSummary{}, fmt.Errorf("account service: get credential: %w", err)
	}
	password, err := s.openPassword(enc)
	if err != nil {
		return AccountSummary{}, err
	}

	worker, _, err := s.leaseWorker(ctx, acc)
	if err != nil {
		// A bare "conflict" tells the operator nothing and gives them nothing
		// to act on. Name the account that holds the machine and the action
		// that frees it: on a serial login machine there is exactly one.
		if errors.Is(err, domain.ErrConflict) {
			return AccountSummary{}, fmt.Errorf(
				"%w: %s", domain.ErrConflict, s.describeLeaseConflict(ctx, acc))
		}
		return AccountSummary{}, fmt.Errorf("account service: lease worker for login: %w", err)
	}
	spec, err := s.pool.RuntimeSpec(ctx, acc)
	if err != nil {
		return AccountSummary{}, err
	}
	spec.Credentials = &port.LoginCreds{Username: acc.Username, Password: password}
	if err := s.pool.StartLogin(ctx, worker, spec); err != nil {
		// The lease is released by the outcome path in normal operation; a
		// dispatch that never landed must not strand it.
		if rerr := s.accounts.ReleaseLease(ctx, acc.ID, worker.ID); rerr != nil {
			s.logger.Warn("account service: release lease after failed login", "accountId", acc.ID, "err", rerr)
		}
		if rerr := s.pool.Slots.ReleaseWorkerSlot(ctx, worker.ID, acc.ID); rerr != nil {
			s.logger.Warn("account service: release slot after failed login", "workerId", worker.ID, "err", rerr)
		}
		if errors.Is(err, domain.ErrConflict) {
			// "worker busy" is the transport's words. The operator needs the
			// reason: this account's own live view is still open on that machine,
			// and closing it is the fix.
			return AccountSummary{}, fmt.Errorf(
				"%w: this account's live view is open on %s; close it (or press Release session) before starting a login",
				domain.ErrConflict, worker.Name)
		}
		return AccountSummary{}, err
	}

	a := acc
	a.AuthStatus = domain.AuthAuthenticating
	dispatchedAt := s.clock.Now()
	a.LastChecked = &dispatchedAt
	if _, err := s.accounts.Update(ctx, a); err != nil {
		return AccountSummary{}, fmt.Errorf("account service: mark pending auth: %w", err)
	}
	s.logger.Info("auth-login dispatched to pool", "accountId", accountID, "workerId", worker.ID)
	view := toAccountView(a)
	s.publishAccount(ctx, view)
	return view, nil
}

// describeLeaseConflict explains a refused lease in the operator's terms.
//
// The DB rule (AcquireAccountWorkerLease) refuses when another account already
// owns the worker, so the useful facts are WHO that is and what frees it. Falls
// back to the generic wording when nothing can be named.
func (s *AccountService) describeLeaseConflict(ctx context.Context, acc domain.Account) string {
	if s.pool != nil {
		candidates, err := s.pool.LoginCandidates(ctx)
		if err == nil && len(candidates) > 0 && s.pool.RPC != nil && candidates[0].EndpointURL != nil {
			probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			held, herr := s.pool.RPC.BusyAccount(probeCtx, *candidates[0].EndpointURL)
			cancel()
			if herr == nil && held != "" && held != acc.ID {
				if other, gerr := s.accounts.GetByID(ctx, held); gerr == nil {
					return fmt.Sprintf(
						"the login machine %s is showing @%s; press Login on that account's row to take it over, or clear it from Workers",
						candidates[0].Name, other.Username)
				}
			}
		}
	}
	return "another session holds the login machine; retry once it settles"
}

// openPassword unseals the credential blob for one dispatch.
func (s *AccountService) openPassword(enc []byte) (string, error) {
	if s.sealer == nil {
		return "", fmt.Errorf("%w: credential sealer not configured", domain.ErrUnavailable)
	}
	plain, err := s.sealer.Open(enc)
	if err != nil {
		return "", fmt.Errorf("account service: unseal credential: %w", err)
	}
	return string(plain), nil
}

// submitInputPooled delivers a 2FA / checkpoint code to the worker holding the
// parked login. The worker is resolved from the account's live lease: the lease
// IS the routing table in a stateless fleet.
func (s *AccountService) submitInputPooled(ctx context.Context, accountID, value string) (AccountSummary, error) {
	a, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return AccountSummary{}, fmt.Errorf("account service: get: %w", err)
	}
	if a.AuthStatus != domain.AuthAuthenticating && a.AuthStatus != domain.AuthNeedsInput {
		return AccountSummary{}, fmt.Errorf("%w: account login is not awaiting input", domain.ErrConflict)
	}
	if a.WorkerID == nil {
		return AccountSummary{}, fmt.Errorf("%w: no worker is holding this login any more", domain.ErrConflict)
	}
	w, err := s.workers.GetByID(ctx, *a.WorkerID)
	if err != nil {
		return AccountSummary{}, fmt.Errorf("account service: worker: %w", err)
	}
	if err := s.pool.SubmitAuthInput(ctx, w, a.ID, a.Platform, value); err != nil {
		return AccountSummary{}, err
	}
	s.logger.Info("auth-input dispatched to pool", "accountId", accountID, "workerId", w.ID)
	return toAccountView(a), nil
}

// exportStoredSession unseals the account's persisted cookie for the operator.
//
// SECURITY: the plaintext leaves once, to an owner/admin caller. It is never
// logged, and the sealed bytes never left the database before this call.
func (s *AccountService) exportStoredSession(ctx context.Context, accountID string) (json.RawMessage, error) {
	if s.sessions == nil || s.sealer == nil {
		return nil, fmt.Errorf("%w: session store is not configured", domain.ErrUnavailable)
	}
	sealed, err := s.sessions.GetState(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("account service: read session: %w", err)
	}
	if len(sealed) == 0 {
		return nil, fmt.Errorf("%w: no session is persisted for this account", domain.ErrNotFound)
	}
	plain, err := s.sealer.Open(sealed)
	if err != nil {
		return nil, fmt.Errorf("account service: unseal session: %w", err)
	}
	s.logger.Info("session exported from store", "accountId", accountID)
	return json.RawMessage(plain), nil
}

// importStoredSession seals an operator-supplied cookie into account_session.
//
// It replaces whatever was stored: the operator is pasting this because the
// previous session is dead, so keeping the old one would let a revoked cookie
// shadow a live one. The expiry is left untouched — the payload does not date
// it, and the refresh schedule must survive.
func (s *AccountService) importStoredSession(ctx context.Context, accountID string, session json.RawMessage) error {
	if s.sessions == nil || s.sealer == nil {
		return fmt.Errorf("%w: session store is not configured", domain.ErrUnavailable)
	}
	var probe map[string]json.RawMessage
	if len(session) == 0 || json.Unmarshal(session, &probe) != nil {
		return fmt.Errorf("%w: session must be a JSON object", domain.ErrValidation)
	}
	if _, err := s.accounts.GetByID(ctx, accountID); err != nil {
		return fmt.Errorf("account service: get: %w", err)
	}
	sealed, err := s.sealer.Seal(session)
	if err != nil {
		return fmt.Errorf("account service: seal session: %w", err)
	}
	// RefreshState, not Save: the operator's paste carries no cookie date, and
	// Save would overwrite the stored expiry with NULL — dropping the account
	// out of the refresh sweep until its next login.
	if err := s.sessions.RefreshState(ctx, accountID, sealed, nil, nil); err != nil {
		return fmt.Errorf("account service: store session: %w", err)
	}
	s.logger.Info("session imported into store", "accountId", accountID)
	return nil
}

// parkedLoginIsLive reports whether the worker holding a parked login still has
// a browser for it. Unreachable or idle workers count as dead: the operator's
// prompt cannot exist on a machine that is not showing anything.
func (s *AccountService) parkedLoginIsLive(ctx context.Context, acc domain.Account) bool {
	if acc.WorkerID == nil || s.pool == nil || s.pool.RPC == nil || s.workers == nil {
		return false
	}
	w, err := s.workers.GetByID(ctx, *acc.WorkerID)
	if err != nil || w.EndpointURL == nil || *w.EndpointURL == "" {
		return false
	}
	// Ask WHO holds the display, not merely whether the machine is up. A worker
	// that is healthy but is holding no context (or a different account) cannot
	// be showing this operator's prompt, and treating liveness as proof kept a
	// dead parked login alive forever.
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	held, err := s.pool.RPC.BusyAccount(probeCtx, *w.EndpointURL)
	if err != nil {
		return false
	}
	return held == acc.ID
}
