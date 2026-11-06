package main

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	oec "github.com/OI4/oec/sdk/go"
)

// The defaults of the application Config (A-302) are the defaults of its
// definition.
func TestSettingsDefaults(t *testing.T) {
	d := defaultSettings()
	want := settings{Provider: "simulation", PollInterval: 60 * time.Second, Stations: 3,
		FrostThreshold: 0, StormThreshold: 20.8, Seed: 1}
	if d != want {
		t.Fatalf("defaults %+v, want %+v", d, want)
	}
	def := appConfig()
	if def.Context != appContext || len(def.Groups) != 1 || def.Groups[0].Name != "common" {
		t.Fatalf("definition %+v", def)
	}
	got, err := parseSettings(values(def))
	if err != nil || got != d {
		t.Errorf("defaults of the definition %+v, %v", got, err)
	}
	if got, err := parseSettings(unset(def)); err != nil || got != d {
		t.Errorf("unset values %+v, %v", got, err)
	}
}

// A complete valid proposal and the refused ones (the checks the SDK does
// by Min/Max/Pattern/Values are repeated: a stored value of an older
// definition may violate them).
func TestParseSettings(t *testing.T) {
	v := values(appConfig())
	v["common"]["pollInterval"] = "5"
	v["common"]["stations"] = "5"
	v["common"]["frostThreshold"] = "-2.5"
	v["common"]["stormThreshold"] = "0"
	v["common"]["simulationSeed"] = "42"
	v["common"]["userAgentContact"] = "oec@example.com"
	got, err := parseSettings(v)
	want := settings{Provider: "simulation", UserAgentContact: "oec@example.com", PollInterval: 5 * time.Second,
		Stations: 5, FrostThreshold: -2.5, StormThreshold: 0, Seed: 42}
	if err != nil || got != want {
		t.Fatalf("%+v, %v; want %+v", got, err, want)
	}
	for name, bad := range map[string][]string{
		"provider":         {"SIMULATION", "met", "metno"},
		"userAgentContact": {"a b@example.org", "x\r\ny", "café@example.org"},
		"pollInterval":     {"0", "0.5", "86401", "ten"},
		"stations":         {"0", "6", "2.5", "x"},
		"frostThreshold":   {"-51", "51", "cold"},
		"stormThreshold":   {"-1", "101"},
		"simulationSeed":   {"-1", "1.5", "1000000000"},
	} {
		for _, value := range bad {
			v := values(appConfig())
			v["common"][name] = value
			got, err := parseSettings(v)
			if err == nil {
				t.Errorf("%s=%q accepted", name, value)
				continue
			}
			var code oec.StatusCode
			if !errors.As(err, &code) || !code.IsBad() {
				t.Errorf("%s=%q: %v is no Bad StatusCode", name, value, err)
			}
			if got != defaultSettings() {
				t.Errorf("%s=%q: %+v, want the defaults", name, value, got)
			}
		}
	}
}

// The provider met-no (ADR-0010, A-302): it needs a userAgentContact
// (MET Norway terms) and a poll interval of at least 600 s. A proposal
// without them is refused; stored values fall back to the simulation.
func TestParseSettingsMetNo(t *testing.T) {
	v := values(appConfig())
	v["common"]["provider"] = "met-no"
	v["common"]["userAgentContact"] = testContact
	v["common"]["pollInterval"] = "600"
	got, err := parseSettings(v)
	want := defaultSettings()
	want.Provider, want.UserAgentContact, want.PollInterval = "met-no", testContact, 600*time.Second
	if err != nil || got != want {
		t.Fatalf("%+v, %v; want %+v", got, err, want)
	}
	for name, c := range map[string]struct {
		contact, poll string
		code          oec.StatusCode
	}{
		"no contact":      {"", "600", oec.BadInvalidArgument},
		"599 s":           {testContact, "599", oec.BadOutOfRange},
		"default 60 s":    {testContact, "", oec.BadOutOfRange},
		"contact refused": {"a b", "600", oec.BadInvalidArgument},
	} {
		v := values(appConfig())
		v["common"]["provider"] = "met-no"
		v["common"]["userAgentContact"] = c.contact
		v["common"]["pollInterval"] = c.poll
		got, err := parseSettings(v)
		var code oec.StatusCode
		if !errors.As(err, &code) || code != c.code {
			t.Errorf("%s: %v, want %v", name, err, c.code)
		}
		if got.Provider != "simulation" {
			t.Errorf("%s: provider %q, want the simulation", name, got.Provider)
		}
	}
	if minMetNoInterval != 600*time.Second {
		t.Errorf("minimum %v", minMetNoInterval)
	}
	provider := appConfig().Groups[0].Objects[0]
	if provider.Name != "provider" || len(provider.OneOf) != 2 || provider.OneOf[1] != "met-no" ||
		!strings.Contains(provider.Description, "MET Norway") || !strings.Contains(provider.Description, "CC BY 4.0") {
		t.Errorf("provider %+v", provider)
	}
}

// Five stations with their default locations; the station Config
// definition carries them as defaults.
func TestStationDefaults(t *testing.T) {
	names := map[string]bool{}
	for i := 1; i <= maxStations; i++ {
		loc := defaultLocation(i)
		if loc.Name == "" || names[loc.Name] {
			t.Errorf("station %d: name %q", i, loc.Name)
		}
		names[loc.Name] = true
		def := stationConfig(i)
		if def.Context != stationContext {
			t.Errorf("context %q", def.Context)
		}
		got, err := parseLocation(i, values(def))
		if err != nil || got != loc {
			t.Errorf("station %d: definition %+v, %v; want %+v", i, got, err, loc)
		}
		if got, err := parseLocation(i, unset(def)); err != nil || got != loc {
			t.Errorf("station %d: unset %+v, %v", i, got, err)
		}
	}
	if defaultLocation(1) != frankfurt || defaultLocation(2) != oslo || defaultLocation(3) != zugspitze {
		t.Errorf("defaults %+v %+v %+v", defaultLocation(1), defaultLocation(2), defaultLocation(3))
	}
}

func TestParseLocation(t *testing.T) {
	v := values(stationConfig(2))
	v["common"]["name"] = "Bergen"
	v["common"]["latitude"] = "60.3913"
	v["common"]["longitude"] = "5.3221"
	v["common"]["altitude"] = "12"
	want := Location{Name: "Bergen", Latitude: 60.3913, Longitude: 5.3221, Altitude: 12}
	if got, err := parseLocation(2, v); err != nil || got != want {
		t.Fatalf("%+v, %v", got, err)
	}
	for name, bad := range map[string][]string{
		"latitude":  {"90.0001", "-91", "50.12345", "1e1", "north"},
		"longitude": {"180.5", "-181", "8.68211"},
		"altitude":  {"-501", "9001", "12.5"},
	} {
		for _, value := range bad {
			v := values(stationConfig(2))
			v["common"][name] = value
			if got, err := parseLocation(2, v); err == nil {
				t.Errorf("%s=%q accepted: %+v", name, value, got)
			}
		}
	}
}

// The Patterns of the definitions (unanchored RE2-like syntax, as the SDK
// checks them) accept the valid and refuse the invalid forms.
func TestPatterns(t *testing.T) {
	objects := map[string]oec.ConfigObject{}
	for _, def := range []oec.ConfigDef{appConfig(), stationConfig(1)} {
		for _, o := range def.Groups[0].Objects {
			objects[o.Name] = o
		}
	}
	for name, cases := range map[string]map[string]bool{
		"latitude":       {"50.1109": true, "-90": true, "0": true, "50.12345": false, "1e1": false, "+5": false, "5.": false},
		"longitude":      {"-180": true, "8.6821": true, "151.2093": true, "8.68211": false, "1234": false},
		"altitude":       {"2962": true, "-12": true, "12.5": false},
		"stations":       {"1": true, "5": true, "2.5": false, "12": false},
		"pollInterval":   {"60": true, "86400": true, "1.5": false},
		"simulationSeed": {"0": true, "123456789": true, "-1": false, "1.5": false},
		"userAgentContact": {testContact: true, "https://example.org/oec": true, "a b": false, " x": false,
			"x\r\nX-Evil: 1": false, "café@example.org": false},
	} {
		o, ok := objects[name]
		if !ok || o.Pattern == "" {
			t.Errorf("%s: no Pattern", name)
			continue
		}
		re := regexp.MustCompile(o.Pattern)
		for value, want := range cases {
			if re.MatchString(value) != want {
				t.Errorf("%s Pattern %q on %q: %v", name, o.Pattern, value, !want)
			}
		}
	}
}

// The container name comes from the hostname of the container storage
// (proc first, as the SDK, REQ-CNT-021).
func TestHostname(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("etc/hostname", "weather-etc\n")
	if h := hostname(root); h != "weather-etc" {
		t.Errorf("etc: %q", h)
	}
	write("proc/sys/kernel/hostname", " weather-1\n")
	if h := hostname(root); h != "weather-1" {
		t.Errorf("proc: %q", h)
	}
	if got := stationSerial("weather-1", 3); got != "weather-1-station-3" {
		t.Errorf("serial %q", got)
	}
}

// values are the Default values of a definition by group and name.
func values(def oec.ConfigDef) oec.ConfigValues {
	v := oec.ConfigValues{}
	for _, g := range def.Groups {
		v[g.Name] = map[string]string{}
		for _, o := range g.Objects {
			v[g.Name][o.Name] = o.Default
		}
	}
	return v
}

// unset are all values of a definition unset ("").
func unset(def oec.ConfigDef) oec.ConfigValues {
	v := values(def)
	for _, g := range v {
		for name := range g {
			g[name] = ""
		}
	}
	return v
}
