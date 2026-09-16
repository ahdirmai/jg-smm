package domain

import (
	"math"
	"math/rand"
	"strings"
)

// City is one selectable location a worker can operate from. RadiusKm is how
// far a randomized point may drift from the centre — a real account is not
// pinned to a single coordinate, but it stays inside its city.
type City struct {
	Name      string
	Latitude  float64
	Longitude float64
	RadiusKm  float64
}

// Cities is the reference list the API owns and the dashboard dropdown renders.
// MVP is Indonesia-only, so region is effectively a constant; the meaningful
// variation is which city a worker is anchored to. Add a city here and it is
// live in the dropdown and the validator at once.
var Cities = []City{
	{Name: "Jakarta", Latitude: -6.2088, Longitude: 106.8456, RadiusKm: 25},
	{Name: "Bandung", Latitude: -6.9175, Longitude: 107.6191, RadiusKm: 20},
	{Name: "Surabaya", Latitude: -7.2575, Longitude: 112.7521, RadiusKm: 20},
	{Name: "Medan", Latitude: 3.5952, Longitude: 98.6722, RadiusKm: 18},
	{Name: "Semarang", Latitude: -6.9667, Longitude: 110.4167, RadiusKm: 15},
	{Name: "Yogyakarta", Latitude: -7.7956, Longitude: 110.3695, RadiusKm: 15},
	{Name: "Malang", Latitude: -7.9666, Longitude: 112.6326, RadiusKm: 15},
	{Name: "Makassar", Latitude: -5.1477, Longitude: 119.4327, RadiusKm: 18},
	{Name: "Palembang", Latitude: -2.9761, Longitude: 104.7754, RadiusKm: 15},
	{Name: "Denpasar", Latitude: -8.6705, Longitude: 115.2126, RadiusKm: 15},
	{Name: "Balikpapan", Latitude: -1.2379, Longitude: 116.8529, RadiusKm: 15},
	{Name: "Pekanbaru", Latitude: 0.5071, Longitude: 101.4478, RadiusKm: 15},
}

// FindCity returns the city by (case-insensitive) name and whether it exists.
func FindCity(name string) (City, bool) {
	for _, c := range Cities {
		if strings.EqualFold(c.Name, name) {
			return c, true
		}
	}
	return City{}, false
}

// RandomPoint picks one point within RadiusKm of the city centre. It is called
// once per worker at create time and then frozen, so a worker keeps a stable
// GPS fingerprint. The offset uses the equirectangular approximation: at city
// scale the curvature error is well under the radius.
func (c City) RandomPoint(rnd *rand.Rand) (lat, lng float64) {
	// km per degree; the longitude factor shrinks toward the poles.
	const kmPerDegLat = 111.32
	kmPerDegLng := 111.32 * math.Cos(c.Latitude*math.Pi/180)

	// Square-root sampling keeps the density uniform over the disc instead of
	// piling points at the centre.
	r := c.RadiusKm * math.Sqrt(rnd.Float64())
	theta := rnd.Float64() * 2 * math.Pi

	dLat := (r * math.Sin(theta)) / kmPerDegLat
	dLng := (r * math.Cos(theta)) / kmPerDegLng

	return roundCoord(c.Latitude + dLat), roundCoord(c.Longitude + dLng)
}

// roundCoord trims to 5 decimals (~1 m), the precision a real GPS fix reports.
func roundCoord(v float64) float64 {
	return math.Round(v*1e5) / 1e5
}
