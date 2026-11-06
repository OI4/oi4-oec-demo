package main

import (
	"context"
	"math"
	"time"
)

// Simulation is the offline provider: the weather is a deterministic
// function of the seed, the coordinates, the altitude and the time – a
// climate by latitude and altitude with a seasonal and a diurnal cycle
// plus smooth value noise. No network, no state: the same inputs always
// give the same Observation (A-343).
type Simulation struct{ Seed uint64 }

// Name of the provider as in the Config object provider.
func (Simulation) Name() string { return "simulation" }

// The noise channels: one per quantity and period.
const (
	chTemperatureSlow = iota + 1
	chTemperatureFast
	chHumiditySlow
	chHumidityFast
	chPressureSlow
	chPressureFast
	chWindSlow
	chWindFast
	chDirectionSlow
	chDirectionFast
)

const (
	hour      = 3600.0
	day       = 24 * hour
	tropical  = 365.2422 * day
	coldest   = 947894400.0 // 2000-01-15T00:00:00Z, the coldest time of the northern year
	hottestAt = 15.0        // local solar hour of the daily maximum
)

// Observe is the simulated weather at loc and at.
func (s Simulation) Observe(ctx context.Context, loc Location, at time.Time) (Observation, error) {
	if err := ctx.Err(); err != nil {
		return Observation{}, err
	}
	key := mix(mix(mix(s.Seed^0x5eed)^uint64(int64(math.Round(loc.Latitude*1e4)))) ^ uint64(int64(math.Round(loc.Longitude*1e4))))
	key = mix(key ^ uint64(int64(math.Round(loc.Altitude))))
	sec := float64(at.UnixNano()) / 1e9
	n := func(ch uint64, period float64) float64 { return noise(key, ch, sec/period) }

	lat := math.Abs(loc.Latitude)
	hemisphere := 1.0
	if loc.Latitude < 0 {
		hemisphere = -1
	}
	season := -math.Cos(2*math.Pi*(sec-coldest)/tropical) * hemisphere // -1 coldest … +1 hottest
	solar := math.Mod(sec/hour+loc.Longitude/15+48, 24)                // local solar hour
	daily := math.Cos(2 * math.Pi * (solar - hottestAt) / 24)          // +1 in the afternoon

	t := 27 - 0.36*lat + 0.2*lat*season + 4*daily - 0.0065*loc.Altitude +
		3*n(chTemperatureSlow, day) + n(chTemperatureFast, 3*hour)
	h := 72 - 15*daily + 12*n(chHumiditySlow, day) + 4*n(chHumidityFast, 3*hour)
	p := 1013.25 + 12*n(chPressureSlow, 1.5*day) + 1.5*n(chPressureFast, 3*hour)
	w := (3 + 0.03*lat + math.Max(loc.Altitude, 0)/400) * (1 + 0.5*n(chWindSlow, day) + 0.25*n(chWindFast, hour))
	d := 240 + 100*n(chDirectionSlow, day) + 25*n(chDirectionFast, 3*hour)

	o := Observation{Time: at, Temperature: t, Humidity: h, Pressure: p, WindSpeed: w, WindDirection: d}
	for _, q := range quantities {
		v := q.value(o)
		if q.Name == "Sv4" {
			v = math.Mod(math.Mod(math.Round(v), 360)+360, 360)
		}
		v = math.Min(math.Max(v, q.Range.Low), q.Range.High)
		*field(&o, q.Name) = round(v, q.resolution)
	}
	return o, nil
}

// field is the address of a value of an Observation by its field name.
func field(o *Observation, name string) *float64 {
	switch name {
	case "Pv":
		return &o.Temperature
	case "Sv1":
		return &o.Humidity
	case "Sv2":
		return &o.Pressure
	case "Sv3":
		return &o.WindSpeed
	default:
		return &o.WindDirection
	}
}

// round rounds to a resolution of 1 or 0.1: an integer divided by 10 is
// the double nearest to the decimal, so the JSON form stays short.
func round(v, resolution float64) float64 {
	if resolution >= 1 {
		return math.Round(v)
	}
	return math.Round(v*10) / 10
}

// noise is smooth value noise in [-1, 1]: random values at the integer
// knots of x, interpolated with smoothstep.
func noise(key, channel uint64, x float64) float64 {
	k := math.Floor(x)
	f := x - k
	a, b := knot(key, channel, int64(k)), knot(key, channel, int64(k)+1)
	return a + (b-a)*f*f*(3-2*f)
}

func knot(key, channel uint64, k int64) float64 {
	r := mix(mix(key^channel*0x9e3779b97f4a7c15) ^ uint64(k))
	return float64(r>>11)/float64(1<<52) - 1
}

// mix is the finalizer of SplitMix64.
func mix(z uint64) uint64 {
	z += 0x9e3779b97f4a7c15
	z = (z ^ z>>30) * 0xbf58476d1ce4e5b9
	z = (z ^ z>>27) * 0x94d049bb133111eb
	return z ^ z>>31
}
