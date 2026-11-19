package service

import (
	"context"
	"errors"
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// healthRPC embeds the interface so only the two methods this path uses have
// to be implemented; the rest would panic if called, and they are not.
type healthRPC struct {
	port.WorkerRPC
	busy  map[string]string
	fail  map[string]error
	pings []string
}

func (h *healthRPC) BusyAccount(_ context.Context, endpoint string) (string, error) {
	if err := h.fail[endpoint]; err != nil {
		return "", err
	}
	return h.busy[endpoint], nil
}

func (h *healthRPC) PingKeepalive(_ context.Context, endpoint string) error {
	h.pings = append(h.pings, endpoint)
	return h.fail[endpoint]
}

func TestWorkerHealthReportsEveryConfiguredWorker(t *testing.T) {
	store := newFakeWorkerStore()
	store.seed(domain.Worker{ID: "w1", Name: "smm-worker-01", EndpointURL: new("https://rpc.one")})
	store.seed(domain.Worker{ID: "w2", Name: "smm-worker-02", EndpointURL: new("https://rpc.two")})
	store.seed(domain.Worker{ID: "w3", Name: "smm-login-01", EndpointURL: new("https://rpc.login")})
	rpc := &healthRPC{
		busy: map[string]string{"https://rpc.one": "acct-1"},
		fail: map[string]error{"https://rpc.two": errors.New("dial timeout")},
	}
	svc := NewContainerService(store, newFakeAccountStore(), nil, ContainerConfig{RPC: rpc})

	health, err := svc.WorkerHealth(context.Background())
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if len(health) != 3 {
		t.Fatalf("got %d workers, want 3", len(health))
	}
	byName := map[string]WorkerHealth{}
	for _, w := range health {
		byName[w.Name] = w
	}
	if !byName["smm-worker-01"].Awake || byName["smm-worker-01"].AccountID == nil || *byName["smm-worker-01"].AccountID != "acct-1" {
		t.Fatalf("worker-01 = %+v, want awake with account", byName["smm-worker-01"])
	}
	if byName["smm-worker-02"].Awake || byName["smm-worker-02"].Error == "" {
		t.Fatalf("worker-02 = %+v, want unreachable with error", byName["smm-worker-02"])
	}
	if !byName["smm-login-01"].Awake {
		t.Fatalf("login worker should be awake: %+v", byName["smm-login-01"])
	}
}

func TestWakeWorkersPingsEveryEndpointAndReturnsHealth(t *testing.T) {
	store := newFakeWorkerStore()
	store.seed(domain.Worker{ID: "w1", Name: "one", EndpointURL: new("https://rpc.one")})
	store.seed(domain.Worker{ID: "w2", Name: "two", EndpointURL: new("https://rpc.two")})
	store.seed(domain.Worker{ID: "w3", Name: "no-endpoint"})
	rpc := &healthRPC{}
	svc := NewContainerService(store, newFakeAccountStore(), nil, ContainerConfig{RPC: rpc})

	health, err := svc.WakeWorkers(context.Background())
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if len(rpc.pings) != 2 {
		t.Fatalf("pings = %v, want both reachable endpoints", rpc.pings)
	}
	if len(health) != 3 {
		t.Fatalf("health returned %d workers, want all three", len(health))
	}
	for _, w := range health {
		if w.Name == "no-endpoint" && w.Error == "" {
			t.Fatalf("a worker without an endpoint must report why: %+v", w)
		}
	}
}
