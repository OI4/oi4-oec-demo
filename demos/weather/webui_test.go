//go:build unix

package main

import (
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/OI4/oec/demos/internal/demotest"
)

// uiStateJSON is GET /api/state and the event "state" of the Web UI.
type uiStateJSON struct {
	Type                string  `json:"type"`
	State               string  `json:"state"`
	Version             string  `json:"version"`
	Provider            string  `json:"provider"`
	PollIntervalSeconds float64 `json:"pollIntervalSeconds"`
	FrostThreshold      float64 `json:"frostThreshold"`
	StormThreshold      float64 `json:"stormThreshold"`
	Health              string  `json:"health"`
	HealthScore         int     `json:"healthScore"`
	Attribution         *struct {
		Text       string `json:"text"`
		Source     string `json:"source"`
		SourceURL  string `json:"sourceUrl"`
		License    string `json:"license"`
		LicenseURL string `json:"licenseUrl"`
	} `json:"attribution"`
	Stations []struct {
		Index        int          `json:"index"`
		Name         string       `json:"name"`
		Latitude     float64      `json:"latitude"`
		Longitude    float64      `json:"longitude"`
		Altitude     float64      `json:"altitude"`
		SerialNumber string       `json:"serialNumber"`
		Health       string       `json:"health"`
		HealthScore  int          `json:"healthScore"`
		Failed       bool         `json:"failed"`
		Error        string       `json:"error"`
		Frost        bool         `json:"frost"`
		Storm        bool         `json:"storm"`
		Observation  *uiObsJSON   `json:"observation"`
		History      []*uiObsJSON `json:"history"`
	} `json:"stations"`
	Updated string `json:"updated"`
}

type uiObsJSON struct {
	Time          string   `json:"time"`
	Temperature   *float64 `json:"temperature"`
	Humidity      *float64 `json:"humidity"`
	Pressure      *float64 `json:"pressure"`
	WindSpeed     *float64 `json:"windSpeed"`
	WindDirection *float64 `json:"windDirection"`
}

// uiState waits until GET /api/state matches ok and returns it.
func (r *rig) uiState(t *testing.T, what string, ok func(uiStateJSON) bool) uiStateJSON {
	t.Helper()
	ui := demotest.WaitUI(t, r.ui)
	var s uiStateJSON
	demotest.Eventually(t, what, func() bool {
		s = uiStateJSON{}
		ui.JSON(t, "/api/state", &s)
		return ok(s)
	})
	return s
}

// The Web UI (ADR-0028 §3, amendment C-083): the embedded files, the
// stations with their values, history and Health, the provider and the
// settings of the OEC Config, live by SSE; the guards and limits common
// to every demo. Read only (no POST).
func TestWebUI(t *testing.T) {
	t.Parallel()
	b := demotest.StartBroker(t)
	r := &rig{b: b, root: b.Container(t, host), ui: demotest.UIAddr(t)}
	r.run(t, 0, demotest.UIKeepAliveEnv+"=200")
	ui := demotest.WaitUI(t, r.ui)
	ui.CheckStatic(t, "OEC Weather")

	ev := ui.Events(t)
	first, _ := ev.Event(t, 0, "state", nil)
	if first["provider"] != "simulation" || first["attribution"] != nil {
		t.Errorf("first state %v", first)
	}
	s := r.uiState(t, "online with observations", func(s uiStateJSON) bool {
		return s.State == "online" && len(s.Stations) == 3 && s.Stations[2].Observation != nil
	})
	if s.Type != "" || s.Version != version || s.Provider != "simulation" || s.PollIntervalSeconds != 60 ||
		s.FrostThreshold != 0 || s.StormThreshold != 20.8 || s.Health != "NORMAL_0" || s.HealthScore != 100 ||
		s.Attribution != nil || s.Updated == "" {
		t.Errorf("state %+v", s)
	}
	for i, st := range s.Stations {
		loc := defaultLocation(i + 1)
		if st.Index != i+1 || st.Name != loc.Name || st.Latitude != loc.Latitude || st.Longitude != loc.Longitude ||
			st.Altitude != loc.Altitude || st.SerialNumber != host+"-station-"+string(rune('1'+i)) ||
			st.Health != "NORMAL_0" || st.HealthScore != 100 || st.Failed || st.Error != "" {
			t.Errorf("station %d %+v", i+1, st)
		}
		o := st.Observation
		if o == nil || o.Temperature == nil || o.Humidity == nil || o.Pressure == nil || o.WindSpeed == nil || o.WindDirection == nil {
			t.Fatalf("station %d observation %+v", i+1, o)
		}
		if _, err := time.Parse(time.RFC3339, o.Time); err != nil || len(st.History) == 0 || !reflect.DeepEqual(st.History[len(st.History)-1], o) {
			t.Errorf("station %d: time %q, history %d", i+1, o.Time, len(st.History))
		}
	}

	// A Set shows live: a fourth station, a new threshold.
	from := ev.Len()
	r.setApp(t, map[string]string{"stations": "4", "stormThreshold": "30"})
	ev.Event(t, from, "state", func(d map[string]any) bool {
		st, _ := d["stations"].([]any)
		return len(st) == 4 && d["stormThreshold"] == 30.0
	})
	cfg, _ := r.publication(t, 0, "Config", station(1), stationContext)
	from = ev.Len()
	if next, status := r.set(t, station(1), stationContext, cfg, map[string]string{"name": "Home"}); next == nil {
		t.Fatalf("Set Config refused: %v", status)
	}
	ev.Event(t, from, "state", func(d map[string]any) bool {
		st, _ := d["stations"].([]any)
		first, _ := st[0].(map[string]any)
		return first["name"] == "Home"
	})
	for _, path := range []string{"/api/state?x=1", "/api/events?x=1"} {
		if resp, raw := ui.Do(t, "GET", path, "", ""); resp.StatusCode != 400 {
			t.Errorf("%s: %d %s", path, resp.StatusCode, raw)
		}
	}
	if resp, raw := ui.Do(t, "POST", "/api/state", "application/json", "{}"); resp.StatusCode != 405 || resp.Header.Get("Allow") != "GET" {
		t.Errorf("POST /api/state: %d %s", resp.StatusCode, raw)
	}

	ui.CheckGuards(t, "/api/state")
	ui.CheckSlow(t)
	ui.CheckKeepAlive(t)
	ev.Close()
	ui.CheckLimits(t, "state")
}

// OEC_UI_LISTEN: empty turns the Web UI off; an address that cannot be
// used stops the service with a message.
func TestWebUIListen(t *testing.T) {
	t.Parallel()
	b := demotest.StartBroker(t)
	r := &rig{b: b, root: b.Container(t, host)}
	r.run(t, 0)
	if !strings.Contains(r.p.Stderr(), "Web UI off") {
		t.Errorf("stderr %s", r.p.Stderr())
	}
	if err := r.p.Stop(t); err != nil {
		t.Errorf("exit after SIGTERM: %v", err)
	}

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	for _, listen := range []string{"no-port", "127.0.0.1:99999", "127.0.0.1:", "[::1:80", ":8782", busy.Addr().String()} {
		p := demotest.Start(t, exe(t), "OEC_ROOT="+r.root, "OEC_UI_LISTEN="+listen)
		if err := p.Exit(t); err == nil || !strings.Contains(p.Stderr(), "Web UI") {
			t.Errorf("%s: exit %v, stderr %s", listen, err, p.Stderr())
		}
	}
	for _, keepAlive := range []string{"0", "15001", "x"} {
		p := demotest.Start(t, exe(t), "OEC_ROOT="+r.root, "OEC_UI_LISTEN=", demotest.UIKeepAliveEnv+"="+keepAlive)
		if err := p.Exit(t); err == nil || !strings.Contains(p.Stderr(), demotest.UIKeepAliveEnv) {
			t.Errorf("%s: exit %v, stderr %s", keepAlive, err, p.Stderr())
		}
	}
}
