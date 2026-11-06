package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	oec "github.com/OI4/oec/sdk/go"
)

// The Config contexts (Filters, REQ-RES-CONFIG-015) of the application and
// of a station, and the number of stations at most (A-302).
const (
	appContext     = "weather"
	stationContext = "station"
	maxStations    = 5
)

// settings are the values of the application Config (A-302, A-342).
type settings struct {
	Provider         string
	UserAgentContact string
	PollInterval     time.Duration
	Stations         int
	FrostThreshold   float64
	StormThreshold   float64
	Seed             uint64
}

func defaultSettings() settings {
	return settings{Provider: "simulation", PollInterval: 60 * time.Second, Stations: 3,
		FrostThreshold: 0, StormThreshold: 20.8, Seed: 1}
}

// The Patterns (unanchored for the SDK, hence ^…$): whole numbers, the
// coordinates with at most 4 decimals (MET Norway terms, A-302).
const (
	patternWhole     = `^[0-9]{1,5}$`
	patternStations  = `^[1-5]$`
	patternSeed      = `^[0-9]{1,9}$`
	patternLatitude  = `^-?[0-9]{1,2}(\.[0-9]{1,4})?$`
	patternLongitude = `^-?[0-9]{1,3}(\.[0-9]{1,4})?$`
	patternAltitude  = `^-?[0-9]{1,4}$`
	// An e-mail address or URL for the User-Agent: printable ASCII without
	// spaces (no header injection).
	patternContact = `^[!-~]+$`
	maxContact     = 256
	maxName        = 64
)

func number(name string, def, min, max float64, pattern, unit, label, description string) oec.ConfigObject {
	o := oec.NumberObject(name, def)
	o.Min, o.Max, o.Pattern, o.Unit, o.Label, o.Description = &min, &max, pattern, unit, label, description
	return o
}

// minMetNoInterval is the shortest poll interval with met-no (A-302):
// a forecast changes about hourly, MET Norway asks for no request before
// Expires.
const minMetNoInterval = 600 * time.Second

// metnoAttribution credits the data of met-no (CC BY 4.0, ADR-0010).
const metnoAttribution = "Based on data from MET Norway (Locationforecast 2.0, api.met.no), licensed under CC BY 4.0 " +
	"(https://creativecommons.org/licenses/by/4.0/); changed: the forecast of the hour nearest to now, mapped to Oi4Pv and rounded"

// appConfig is the definition of the application Config (A-342).
func appConfig() oec.ConfigDef {
	provider := oec.StringObject("provider", "simulation")
	provider.OneOf, provider.Label = []string{"simulation", "met-no"}, "Weather data provider"
	provider.Description = "Source of the weather data: simulation (offline, deterministic) or met-no (MET Norway " +
		"Locationforecast, needs userAgentContact and a pollInterval of at least 600 s). met-no: " + metnoAttribution
	contact := oec.StringObject("userAgentContact", "")
	contact.MaxLength, contact.Pattern, contact.Label = maxContact, patternContact, "User-Agent contact"
	contact.Description = "E-mail address or URL of the operator, sent in the User-Agent to MET Norway (required by its terms for met-no)"
	return oec.ConfigDef{Context: appContext, Label: "Weather service", Groups: []oec.ConfigGroup{{
		Name: "common", Label: "Common settings", Objects: []oec.ConfigObject{
			provider,
			contact,
			number("pollInterval", 60, 1, 86400, patternWhole, "s", "Poll interval", "Time between two observations of all stations (at least 600 s with met-no)"),
			number("stations", 3, 1, maxStations, patternStations, "", "Stations", "Number of weather stations (represented assets)"),
			number("frostThreshold", 0, -50, 50, "", "°C", "Frost threshold", "A temperature at or below raises the event Frost"),
			number("stormThreshold", 20.8, 0, 100, "", "m/s", "Storm threshold", "A wind speed at or above raises the event Storm"),
			number("simulationSeed", 1, 0, 999999999, patternSeed, "", "Simulation seed", "Seed of the simulated weather"),
		},
	}}}
}

// parseSettings reads a complete proposal or the stored values; "" is the
// default (REQ-RES-CONFIG-010). The checks of the definition are repeated
// (a stored value of an older definition may violate them): an invalid
// value is replaced by its default and reported as the first error.
func parseSettings(v oec.ConfigValues) (settings, error) {
	s := defaultSettings()
	p := parser{values: v["common"]}
	if x, ok := p.oneOf("provider", "simulation", "met-no"); ok {
		s.Provider = x
	}
	if x, ok := p.text("userAgentContact", maxContact, patternContact); ok {
		s.UserAgentContact = x
	}
	if x, ok := p.number("pollInterval", 1, 86400, patternWhole); ok {
		s.PollInterval = time.Duration(x) * time.Second
	}
	if x, ok := p.number("stations", 1, maxStations, patternStations); ok {
		s.Stations = int(x)
	}
	if x, ok := p.number("frostThreshold", -50, 50, ""); ok {
		s.FrostThreshold = x
	}
	if x, ok := p.number("stormThreshold", 0, 100, ""); ok {
		s.StormThreshold = x
	}
	if x, ok := p.number("simulationSeed", 0, 999999999, patternSeed); ok {
		s.Seed = uint64(x)
	}
	// The rules of met-no across objects (A-302, ADR-0010): a proposal
	// that breaks them is refused, stored values fall back to the
	// simulation.
	if s.Provider == "met-no" {
		switch {
		case s.UserAgentContact == "":
			p.failf(oec.BadInvalidArgument, "provider met-no needs a userAgentContact (MET Norway terms)")
			s.Provider = "simulation"
		case s.PollInterval < minMetNoInterval:
			p.failf(oec.BadOutOfRange, "provider met-no needs a pollInterval of at least %d s, not %d s",
				int(minMetNoInterval.Seconds()), int(s.PollInterval.Seconds()))
			s.Provider = "simulation"
		}
	}
	return s, p.err
}

// The default stations (A-342).
var defaultLocations = [maxStations]Location{
	{Name: "Frankfurt am Main", Latitude: 50.1109, Longitude: 8.6821, Altitude: 112},
	{Name: "Oslo", Latitude: 59.9139, Longitude: 10.7522, Altitude: 23},
	{Name: "Zugspitze", Latitude: 47.4211, Longitude: 10.9853, Altitude: 2962},
	{Name: "Hamburg", Latitude: 53.5511, Longitude: 9.9937, Altitude: 6},
	{Name: "Munich", Latitude: 48.1374, Longitude: 11.5755, Altitude: 519},
}

// defaultLocation is the default of station i (1 … maxStations).
func defaultLocation(i int) Location { return defaultLocations[i-1] }

// stationConfig is the definition of the Config of station i with its
// default location (A-342).
func stationConfig(i int) oec.ConfigDef {
	loc := defaultLocation(i)
	name := oec.StringObject("name", loc.Name)
	name.MaxLength, name.Label, name.Description = maxName, "Name", "Name of the station"
	return oec.ConfigDef{Context: stationContext, Label: "Weather station", Groups: []oec.ConfigGroup{{
		Name: "common", Label: "Location", Objects: []oec.ConfigObject{
			name,
			number("latitude", loc.Latitude, -90, 90, patternLatitude, "°", "Latitude", "WGS 84, north positive, at most 4 decimals"),
			number("longitude", loc.Longitude, -180, 180, patternLongitude, "°", "Longitude", "WGS 84, east positive, at most 4 decimals"),
			number("altitude", loc.Altitude, -500, 9000, patternAltitude, "m", "Altitude", "Height above sea level"),
		},
	}}}
}

// parseLocation reads the Config of station i as parseSettings.
func parseLocation(i int, v oec.ConfigValues) (Location, error) {
	loc := defaultLocation(i)
	p := parser{values: v["common"]}
	if x, ok := p.text("name", maxName, ""); ok {
		loc.Name = x
	}
	if x, ok := p.number("latitude", -90, 90, patternLatitude); ok {
		loc.Latitude = x
	}
	if x, ok := p.number("longitude", -180, 180, patternLongitude); ok {
		loc.Longitude = x
	}
	if x, ok := p.number("altitude", -500, 9000, patternAltitude); ok {
		loc.Altitude = x
	}
	return loc, p.err
}

// parser reads values of one group; ok is false for "" (the default) and
// for an invalid value, which sets err once.
type parser struct {
	values map[string]string
	err    error
}

func (p *parser) fail(name, value string, code oec.StatusCode) {
	if p.err == nil {
		p.err = fmt.Errorf("%s %q: %w", name, value, code)
	}
}

func (p *parser) failf(code oec.StatusCode, format string, args ...any) {
	if p.err == nil {
		p.err = fmt.Errorf(format+": %w", append(args, code)...)
	}
}

func (p *parser) text(name string, maxLength int, pattern string) (string, bool) {
	v := p.values[name]
	if v == "" {
		return "", false
	}
	if utf8.RuneCountInString(v) > maxLength {
		p.fail(name, v, oec.BadOutOfRange)
		return "", false
	}
	if pattern != "" && !patterns[pattern].MatchString(v) {
		p.fail(name, v, oec.BadInvalidArgument)
		return "", false
	}
	return v, true
}

func (p *parser) oneOf(name string, allowed ...string) (string, bool) {
	v := p.values[name]
	if v == "" {
		return "", false
	}
	for _, a := range allowed {
		if v == a {
			return v, true
		}
	}
	p.fail(name, v, oec.BadInvalidArgument)
	return "", false
}

// patterns are compiled once; read-only afterwards (handlers run on the
// SDK thread, stations are added by the poll goroutine).
var patterns = func() map[string]*regexp.Regexp {
	m := map[string]*regexp.Regexp{}
	for _, p := range []string{patternWhole, patternStations, patternSeed, patternLatitude, patternLongitude, patternAltitude, patternContact} {
		m[p] = regexp.MustCompile(p)
	}
	return m
}()

func (p *parser) number(name string, min, max float64, pattern string) (float64, bool) {
	v := p.values[name]
	if v == "" {
		return 0, false
	}
	x, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(x) || math.IsInf(x, 0) {
		p.fail(name, v, oec.BadInvalidArgument)
		return 0, false
	}
	if pattern != "" {
		if !patterns[pattern].MatchString(v) {
			p.fail(name, v, oec.BadInvalidArgument)
			return 0, false
		}
	}
	if x < min || x > max {
		p.fail(name, v, oec.BadOutOfRange)
		return 0, false
	}
	return x, true
}

// hostname is the container name: the hostname of the container storage
// below root ("" = "/"), as the SDK reads it (REQ-CNT-021); else the
// hostname of the system. The Go SDK has no getter of the Identification
// Link (A-324).
func hostname(root string) string {
	if root == "" {
		root = "/"
	}
	for _, f := range []string{"proc/sys/kernel/hostname", "etc/hostname"} {
		if b, err := os.ReadFile(filepath.Join(root, f)); err == nil {
			if h := strings.TrimSpace(string(b)); h != "" {
				return h
			}
		}
	}
	h, _ := os.Hostname()
	return h
}

// stationSerial is the SerialNumber of station i: unique per container
// (A-341).
func stationSerial(host string, i int) string {
	return host + "-station-" + strconv.Itoa(i)
}
