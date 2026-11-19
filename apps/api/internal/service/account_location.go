package service

import (
	"fmt"
	"math/rand/v2"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// resolvedLocation is the device identity an account reports to the platform.
type resolvedLocation struct {
	location  *string
	latitude  *float64
	longitude *float64
	timezone  string
}

// resolveLocation turns an operator's city choice (or its absence) into the
// account's own GPS + timezone.
//
// seed is the account identity: the point is derived from it, so an account
// always reports the same coordinate. A city picked by hand is validated against
// the reference list rather than stored verbatim — a typo would otherwise travel
// straight into the browser context as a bogus timezone.
func resolveLocation(city *string, seed string) (resolvedLocation, error) {
	name := ""
	if city != nil {
		name = *city
	}
	var chosen domain.City
	if name == "" {
		// Deterministic by identity: a 100-row import spreads across the fleet's
		// cities instead of piling everyone into the first one.
		chosen = domain.Cities[fnv32a(seed)%uint32(len(domain.Cities))]
	} else {
		c, ok := domain.FindCity(name)
		if !ok {
			return resolvedLocation{}, fmt.Errorf("%w: unknown location %q", domain.ErrValidation, name)
		}
		chosen = c
	}

	hash := fnv32a(seed)
	//nolint:gosec // deterministic on purpose: the same account must keep the
	// same coordinate across restarts, so a seeded stream is the right tool.
	rnd := rand.New(rand.NewPCG(uint64(hash), uint64(hash)^0x9e3779b97f4a7c15))
	lat, lng := chosen.RandomPoint(rnd)

	return resolvedLocation{
		location:  new(chosen.Name),
		latitude:  &lat,
		longitude: &lng,
		timezone:  domain.TimezoneForCity(chosen.Name),
	}, nil
}
