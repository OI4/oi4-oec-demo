package main

import (
	"context"
	"math"
	"testing"
	"time"
)

var (
	frankfurt = Location{Name: "Frankfurt am Main", Latitude: 50.1109, Longitude: 8.6821, Altitude: 112}
	oslo      = Location{Name: "Oslo", Latitude: 59.9139, Longitude: 10.7522, Altitude: 23}
	zugspitze = Location{Name: "Zugspitze", Latitude: 47.4211, Longitude: 10.9853, Altitude: 2962}
	sydney    = Location{Name: "Sydney", Latitude: -33.8688, Longitude: 151.2093, Altitude: 58}
)

func observe(t *testing.T, p Provider, loc Location, at time.Time) Observation {
	t.Helper()
	o, err := p.Observe(context.Background(), loc, at)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// The same seed, location and time give the same observation; another
// seed or another location gives another one; the name does not matter.
func TestSimulationDeterministic(t *testing.T) {
	at := time.Date(2026, 11, 1, 12, 34, 56, 0, time.UTC)
	a := observe(t, Simulation{Seed: 1}, frankfurt, at)
	b := observe(t, Simulation{Seed: 1}, frankfurt, at)
	if a != b {
		t.Fatalf("not deterministic: %+v %+v", a, b)
	}
	if c := observe(t, Simulation{Seed: 2}, frankfurt, at); c == a {
		t.Errorf("seed has no effect: %+v", c)
	}
	moved := frankfurt
	moved.Latitude += 0.5
	if c := observe(t, Simulation{Seed: 1}, moved, at); c == a {
		t.Errorf("location has no effect: %+v", c)
	}
	renamed := frankfurt
	renamed.Name = "FRA"
	if c := observe(t, Simulation{Seed: 1}, renamed, at); c != a {
		t.Errorf("the name changes the weather: %+v", c)
	}
	if !a.Time.Equal(at) {
		t.Errorf("Time %v, want %v", a.Time, at)
	}
	if name := (Simulation{}).Name(); name != "simulation" {
		t.Errorf("Name %q", name)
	}
}

// Every value stays within the Range of its field (the Metadata) for
// many places and times; it is rounded to the resolution of the field.
func TestSimulationRanges(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for seed := uint64(0); seed < 4; seed++ {
		for lat := -90.0; lat <= 90; lat += 15 {
			for lon := -180.0; lon <= 180; lon += 45 {
				for _, alt := range []float64{-400, 0, 1500, 8800} {
					loc := Location{Latitude: lat, Longitude: lon, Altitude: alt}
					for h := 0; h < 365*24; h += 97 {
						o := observe(t, Simulation{Seed: seed}, loc, start.Add(time.Duration(h)*time.Hour))
						for _, q := range quantities {
							v := q.value(o)
							if math.IsNaN(v) || v < q.Range.Low || v > q.Range.High {
								t.Fatalf("%s = %v outside %v at %+v %v", q.Name, v, *q.Range, loc, o.Time)
							}
							if r := math.Round(v/q.resolution) * q.resolution; math.Abs(r-v) > 1e-9 {
								t.Fatalf("%s = %v not rounded to %v", q.Name, v, q.resolution)
							}
						}
					}
				}
			}
		}
	}
}

func mean(t *testing.T, loc Location, from time.Time, days int, value func(Observation) float64) float64 {
	t.Helper()
	sum, n := 0.0, 0
	for h := 0; h < days*24; h += 3 {
		sum += value(observe(t, Simulation{Seed: 7}, loc, from.Add(time.Duration(h)*time.Hour)))
		n++
	}
	return sum / float64(n)
}

func temperature(o Observation) float64 { return o.Temperature }

// A plausible climate: summer warmer than winter (the other way round in
// the south), the north and the mountain colder, more wind on the
// mountain, a plausible mean pressure.
func TestSimulationClimate(t *testing.T) {
	jan := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	jul := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	fraJan, fraJul := mean(t, frankfurt, jan, 31, temperature), mean(t, frankfurt, jul, 31, temperature)
	if fraJan < -6 || fraJan > 6 || fraJul < 14 || fraJul > 24 {
		t.Errorf("Frankfurt January %.1f °C, July %.1f °C", fraJan, fraJul)
	}
	if osloJan := mean(t, oslo, jan, 31, temperature); osloJan >= fraJan {
		t.Errorf("Oslo January %.1f °C not colder than Frankfurt %.1f °C", osloJan, fraJan)
	}
	if zugJul := mean(t, zugspitze, jul, 31, temperature); zugJul > fraJul-12 {
		t.Errorf("Zugspitze July %.1f °C, Frankfurt %.1f °C", zugJul, fraJul)
	}
	if sydJan, sydJul := mean(t, sydney, jan, 31, temperature), mean(t, sydney, jul, 31, temperature); sydJan <= sydJul {
		t.Errorf("Sydney January %.1f °C, July %.1f °C", sydJan, sydJul)
	}
	wind := func(o Observation) float64 { return o.WindSpeed }
	if fra, zug := mean(t, frankfurt, jan, 31, wind), mean(t, zugspitze, jan, 31, wind); zug <= fra {
		t.Errorf("wind Zugspitze %.1f m/s, Frankfurt %.1f m/s", zug, fra)
	}
	pressure := func(o Observation) float64 { return o.Pressure }
	if p := mean(t, frankfurt, jan, 31, pressure); p < 1000 || p > 1025 {
		t.Errorf("pressure %.1f hPa", p)
	}
}

// The weather changes smoothly: one minute later the values differ
// little, on other days the temperature differs.
func TestSimulationSmooth(t *testing.T) {
	at := time.Date(2026, 4, 12, 9, 0, 0, 0, time.UTC)
	for m := 0; m < 24*60; m += 7 {
		a := observe(t, Simulation{Seed: 3}, frankfurt, at.Add(time.Duration(m)*time.Minute))
		b := observe(t, Simulation{Seed: 3}, frankfurt, at.Add(time.Duration(m+1)*time.Minute))
		if math.Abs(a.Temperature-b.Temperature) > 0.3 || math.Abs(a.Humidity-b.Humidity) > 1.5 ||
			math.Abs(a.Pressure-b.Pressure) > 0.3 || math.Abs(a.WindSpeed-b.WindSpeed) > 0.5 {
			t.Fatalf("jump within a minute: %+v → %+v", a, b)
		}
		if d := math.Abs(a.WindDirection - b.WindDirection); d > 5 && d < 355 {
			t.Fatalf("wind direction jump: %+v → %+v", a, b)
		}
	}
	a := observe(t, Simulation{Seed: 3}, frankfurt, at)
	changed := false
	for d := 1; d < 5; d++ {
		if b := observe(t, Simulation{Seed: 3}, frankfurt, at.Add(time.Duration(d)*24*time.Hour)); b.Temperature != a.Temperature {
			changed = true
		}
	}
	if !changed {
		t.Error("the same temperature on five days")
	}
}

// A canceled context ends Observe (as it would a network provider).
func TestSimulationCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Simulation{}).Observe(ctx, frankfurt, time.Now()); err == nil {
		t.Error("no error")
	}
}
