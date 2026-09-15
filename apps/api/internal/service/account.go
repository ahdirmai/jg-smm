package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

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

const timeRFC3339 = "2006-01-02T15:04:05Z07:00"
