package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// NovncAllocator hands out browser-reachable live-view host ports. Both the
// manual path (ContainerService.Create) and the AUTO path (Packer.createWorker)
// go through it, which is the point: there is one source of truth for which
// ports are taken, so a manual create and an auto-spawn cannot pick the same
// one and cannot diverge from what the docker driver binds.
type NovncAllocator struct {
	workers port.WorkerStore
	host    string
	portMin int
	portMax int
}

// NewNovncAllocator wires the allocator. portMax <= 0 disables allocation:
// Allocate then returns nil and the dashboard shows a disabled Live view
// button instead of a dead link.
func NewNovncAllocator(workers port.WorkerStore, host string, portMin, portMax int) *NovncAllocator {
	if host == "" {
		host = "localhost"
	}
	return &NovncAllocator{workers: workers, host: host, portMin: portMin, portMax: portMax}
}

// Allocate picks a port for a new worker. An explicit request is validated
// against the range and against the ports every existing worker already holds;
// nil takes the first free one. The result is a URL so it can be written
// straight onto the row.
func (a *NovncAllocator) Allocate(ctx context.Context, requested *int) (*string, error) {
	if a.portMax <= 0 {
		return nil, nil
	}
	taken, err := a.UsedPorts(ctx)
	if err != nil {
		return nil, err
	}
	p, err := a.pick(taken, requested)
	if err != nil {
		return nil, err
	}
	return ptrString(fmt.Sprintf("http://%s:%d", a.host, p)), nil
}

// pick validates an explicit request or scans for the first free port.
func (a *NovncAllocator) pick(taken map[int]bool, requested *int) (int, error) {
	if requested != nil {
		if *requested < a.portMin || *requested > a.portMax {
			return 0, fmt.Errorf("%w: novncPort must be in %d-%d", domain.ErrValidation, a.portMin, a.portMax)
		}
		if taken[*requested] {
			return 0, fmt.Errorf("%w: novncPort %d is already used by another container", domain.ErrConflict, *requested)
		}
		return *requested, nil
	}
	for p := a.portMin; p <= a.portMax; p++ {
		if !taken[p] {
			return p, nil
		}
	}
	return 0, fmt.Errorf("%w: no free novnc port in %d-%d", domain.ErrConflict, a.portMin, a.portMax)
}

// UsedPorts is the set of ports already claimed by existing workers, derived
// from the URLs stored on their rows. A malformed value is skipped, not fatal.
func (a *NovncAllocator) UsedPorts(ctx context.Context) (map[int]bool, error) {
	workers, err := a.workers.List(ctx, port.WorkerFilter{Limit: 500})
	if err != nil {
		return nil, fmt.Errorf("novnc allocator: list: %w", err)
	}
	taken := make(map[int]bool, len(workers))
	for _, w := range workers {
		if p := novncPortOf(w.NoVNCService); p > 0 {
			taken[p] = true
		}
	}
	return taken, nil
}

// novncPortOf reads the trailing :port of a stored live-view URL. 0 when the
// value is absent or unparseable.
func novncPortOf(url *string) int {
	if url == nil {
		return 0
	}
	i := strings.LastIndex(*url, ":")
	if i < 0 {
		return 0
	}
	var p int
	if _, err := fmt.Sscanf((*url)[i+1:], "%d", &p); err != nil {
		return 0
	}
	return p
}
