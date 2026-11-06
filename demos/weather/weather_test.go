//go:build unix

// Integration tests of the weather service D3 (ADR-0028 §6): the binary
// against the in-process TLS test broker of the Rust SDK, offline, with
// the simulation provider. The gate runs them against the -race build
// (oec/demos/check.sh).
package main

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OI4/oec/demos/internal/demotest"
)

const host = "weather-1"

// The Identification Link of the weather service (REQ-IL-002) and its
// topic level.
var (
	il = "oi4.org/OEC,20Weather/OEC-WEATHER/" + host
	me = demotest.Topic(il)
)

// station is the Identification Link of station i (an Oi4Identifier,
// REQ-IL-005).
func station(i int) string {
	return "oi4.org/OEC,20Weather,20Station/OEC-WEATHER-STATION/" + host + "-station-" + strconv.Itoa(i)
}

const good = 0 // Status code (MG 8.3.6)

func exe(t *testing.T) string {
	return demotest.Tool(t, "OEC_WEATHER", "demos/weather/target/oec-weather", "go build -o weather/target/oec-weather ./weather in oec/demos")
}

// rig is a weather service on its own test broker.
type rig struct {
	b    *demotest.Broker
	root string
	p    *demotest.Process
	conn int
	// The address of the Web UI (OEC_UI_LISTEN).
	ui string
	// The first Pub Config of the application and the index of the broker
	// events after it.
	config map[string]any
	at     int
}

func start(t *testing.T) *rig {
	t.Helper()
	b := demotest.StartBroker(t)
	r := &rig{b: b, root: b.Container(t, host), ui: demotest.UIAddr(t)}
	r.run(t, 0)
	return r
}

// run starts the weather service (again) with its Web UI on r.ui ("" =
// off) and the environment env and waits for its Pub Config.
func (r *rig) run(t *testing.T, from int, env ...string) {
	t.Helper()
	r.p = demotest.Start(t, exe(t), append([]string{"OEC_ROOT=" + r.root, "OEC_UI_LISTEN=" + r.ui}, env...)...)
	connect, i := r.b.Expect(t, from, "CONNECT of the weather service", func(s demotest.Seen) bool {
		return s.Seen == "connect" && s.ClientID == host
	})
	r.conn = connect.Conn
	pub, j := r.b.Publish(t, i, r.topic("Pub", "Config", il)+"/"+appContext)
	r.config = demotest.AsJSON(t, pub.Payload)
	r.at = j
}

func (r *rig) topic(method, resource, source string) string {
	return "Oi4v2/OTConnector/" + me + "/" + method + "/" + resource + "/" + demotest.Topic(source)
}

// publication waits for a Pub of the resource of source (with the filter,
// "" = none) from index from on.
func (r *rig) publication(t *testing.T, from int, resource, source, filter string) (map[string]any, int) {
	t.Helper()
	topic := r.topic("Pub", resource, source)
	if filter != "" {
		topic += "/" + filter
	}
	s, i := r.b.Publish(t, from, topic)
	return demotest.AsJSON(t, s.Payload), i
}

// set sends a Set Config of source and context with the values of group
// common based on the publication pub; it returns the answering Pub Config
// (nil if the Set is refused) and the Status event.
func (r *rig) set(t *testing.T, source, context string, pub map[string]any, values map[string]string) (map[string]any, map[string]any) {
	t.Helper()
	common := map[string]any{}
	for k, v := range values {
		common[k] = map[string]any{"Value": v}
	}
	from := r.b.Len()
	id := r.b.Request(t, r.conn, r.topic("Set", "Config", source)+"/"+context, pub, map[string]any{"common": common})
	ev, _ := r.b.Expect(t, from, "Status event answering "+id, func(s demotest.Seen) bool {
		return s.Seen == "publish" && strings.HasPrefix(s.Topic, r.topic("Pub", "Event", source)+"/Status/") &&
			strings.Contains(s.Payload, `"ReplyTo":"`+id+`"`)
	})
	status := demotest.AsJSON(t, ev.Payload)
	if demotest.EventNumber(t, status) != good {
		return nil, status
	}
	cfg, _ := r.b.Answer(t, from, r.topic("Pub", "Config", source)+"/"+context, id)
	return cfg, status
}

// setApp sets values of the application Config.
func (r *rig) setApp(t *testing.T, values map[string]string) map[string]any {
	t.Helper()
	cfg, status := r.set(t, il, appContext, r.config, values)
	if cfg == nil {
		t.Fatalf("Set Config %v refused: %v", values, status)
	}
	return cfg
}

func payload(t *testing.T, msg map[string]any) map[string]any {
	t.Helper()
	p, ok := msg["Messages"].([]any)[0].(map[string]any)["Payload"].(map[string]any)
	if !ok {
		t.Fatalf("no Payload in %v", msg)
	}
	return p
}

func configValue(t *testing.T, cfg map[string]any, name string) string {
	t.Helper()
	v, _ := payload(t, cfg)["common"].(map[string]any)[name].(map[string]any)["Value"].(string)
	return v
}

// data checks a Data DataSet Oi4Pv of a station: every field present and
// within the Range of its Metadata.
func data(t *testing.T, msg map[string]any) map[string]float64 {
	t.Helper()
	p := payload(t, msg)
	out := map[string]float64{}
	for _, q := range quantities {
		v, ok := p[q.Name].(float64)
		if !ok || v < q.Range.Low || v > q.Range.High {
			t.Errorf("%s = %v in %v", q.Name, p[q.Name], p)
		}
		out[q.Name] = v
	}
	if len(p) != len(quantities) {
		t.Errorf("fields %v", p)
	}
	return out
}

// published reports whether a publication of source was seen from index
// from on.
func (r *rig) published(from int, source string) bool {
	for _, s := range r.b.Events()[from:] {
		if s.Seen == "publish" && strings.Contains(s.Topic, "/Pub/") && strings.Contains(s.Topic, "/"+demotest.Topic(source)) {
			return true
		}
	}
	return false
}

// The defaults: the application Config (A-302), three stations with MAM,
// Health, Config with the default location, Metadata and Data Oi4Pv
// within the Ranges; no fourth station.
func TestDefaults(t *testing.T) {
	t.Parallel()
	r := start(t)
	for name, want := range map[string]string{
		"provider": "simulation", "userAgentContact": "", "pollInterval": "60", "stations": "3",
		"frostThreshold": "0", "stormThreshold": "20.8", "simulationSeed": "1",
	} {
		if v := configValue(t, r.config, name); v != want {
			t.Errorf("Config %s = %q, want %q", name, v, want)
		}
	}
	mam, _ := r.publication(t, 0, "MAM", il, "")
	if p := payload(t, mam); p["ProductCode"] != "OEC-WEATHER" || p["SerialNumber"] != host {
		t.Errorf("MAM %v", p)
	}
	for i := 1; i <= 3; i++ {
		src := station(i)
		mam, _ := r.publication(t, 0, "MAM", src, "")
		if p := payload(t, mam); p["ProductCode"] != "OEC-WEATHER-STATION" || p["SerialNumber"] != stationSerial(host, i) {
			t.Errorf("station %d: MAM %v", i, p)
		}
		health, _ := r.publication(t, 0, "Health", src, "")
		if p := payload(t, health); p["Health"] != "NORMAL_0" || p["HealthScore"] != 100.0 {
			t.Errorf("station %d: Health %v", i, p)
		}
		cfg, _ := r.publication(t, 0, "Config", src, stationContext)
		loc := defaultLocation(i)
		for name, want := range map[string]string{
			"name": loc.Name, "latitude": strconv.FormatFloat(loc.Latitude, 'f', -1, 64),
			"longitude": strconv.FormatFloat(loc.Longitude, 'f', -1, 64), "altitude": strconv.FormatFloat(loc.Altitude, 'f', -1, 64),
		} {
			if v := configValue(t, cfg, name); v != want {
				t.Errorf("station %d: Config %s = %q, want %q", i, name, v, want)
			}
		}
		meta, _ := r.b.Publish(t, 0, r.topic("Pub", "Metadata", src)+"/Oi4Pv")
		for _, q := range quantities {
			if !strings.Contains(meta.Payload, `"Name":"`+q.Name+`"`) || !strings.Contains(meta.Payload, q.Description) {
				t.Errorf("station %d: Metadata without %s (%s): %s", i, q.Name, q.Description, meta.Payload)
			}
		}
		d, _ := r.publication(t, 0, "Data", src, "Oi4Pv")
		data(t, d)
	}
	if r.published(0, station(4)) {
		t.Error("a fourth station")
	}
	r.p.Line(t, 0, "poll line", func(l string) bool { return strings.HasPrefix(l, "poll 3 stations") })
}

// The number of stations follows the Config: new stations appear with
// their MAM, Config and Data; removed ones leave the PublicationList
// (REQ-RES-PUBLIST-017) and publish nothing more; a station that comes
// back appears again.
func TestStations(t *testing.T) {
	t.Parallel()
	r := start(t)
	from := r.b.Len()
	r.setApp(t, map[string]string{"stations": "5"})
	for i := 4; i <= 5; i++ {
		r.publication(t, from, "MAM", station(i), "")
		cfg, _ := r.publication(t, from, "Config", station(i), stationContext)
		if v := configValue(t, cfg, "name"); v != defaultLocation(i).Name {
			t.Errorf("station %d: name %q", i, v)
		}
		d, _ := r.publication(t, from, "Data", station(i), "Oi4Pv")
		data(t, d)
	}

	from = r.b.Len()
	r.setApp(t, map[string]string{"stations": "1"})
	r.b.Expect(t, from, "PublicationList without stations 2 … 5", func(s demotest.Seen) bool {
		// The complete list is published without Source.
		if s.Seen != "publish" || s.Topic != "Oi4v2/OTConnector/"+me+"/Pub/PublicationList" || !strings.Contains(s.Payload, station(1)) {
			return false
		}
		for i := 2; i <= 5; i++ {
			if strings.Contains(s.Payload, station(i)) {
				return false
			}
		}
		return true
	})
	// The poll round right after the Set publishes only station 1.
	gone := r.b.Len()
	r.p.Line(t, 0, "poll line with one station", func(l string) bool { return strings.HasPrefix(l, "poll 1 stations") })
	// The answer to a Get comes after everything the poll round published.
	r.b.Get(t, r.conn, r.topic("Get", "Health", il), "Health")
	r.b.Publish(t, gone, r.topic("Pub", "Health", il))
	for i := 2; i <= 5; i++ {
		if r.published(gone, station(i)) {
			t.Errorf("station %d published after its removal", i)
		}
	}

	from = r.b.Len()
	r.setApp(t, map[string]string{"stations": "2"})
	r.publication(t, from, "MAM", station(2), "")
	r.publication(t, from, "Data", station(2), "Oi4Pv")
}

// The poll interval: 1 s gives a poll round about every second.
func TestPollInterval(t *testing.T) {
	t.Parallel()
	r := start(t)
	r.setApp(t, map[string]string{"pollInterval": "1"})
	from := r.p.Len()
	_, i := r.p.Line(t, from, "poll line", func(l string) bool { return strings.HasPrefix(l, "poll ") })
	for n := 0; n < 3; n++ {
		_, i = r.p.Line(t, i+1, "next poll line", func(l string) bool { return strings.HasPrefix(l, "poll ") })
	}
}

// Set Config of a station: a new location changes the Data at once; the
// SDK refuses values outside Min/Max or the Pattern (at most 4 decimals,
// A-302) with Status Bad, nothing changes.
func TestStationConfig(t *testing.T) {
	t.Parallel()
	r := start(t)
	src := station(1)
	cfg, _ := r.publication(t, 0, "Config", src, stationContext)
	before, _ := r.publication(t, 0, "Data", src, "Oi4Pv")
	old := data(t, before)

	from := r.b.Len()
	next, status := r.set(t, src, stationContext, cfg, map[string]string{
		"name": "Singapore", "latitude": "1.3521", "longitude": "103.8198", "altitude": "15",
	})
	if next == nil {
		t.Fatalf("Set Config refused: %v", status)
	}
	if v := configValue(t, next, "name"); v != "Singapore" {
		t.Errorf("name %q", v)
	}
	after, _ := r.publication(t, from, "Data", src, "Oi4Pv")
	if now := data(t, after); now["Pv"] == old["Pv"] && now["Sv1"] == old["Sv1"] && now["Sv2"] == old["Sv2"] {
		t.Errorf("the same weather in Singapore: %v %v", old, now)
	}
	r.p.Line(t, 0, "applied line", func(l string) bool { return strings.Contains(l, "station 1 Singapore") })

	for name, value := range map[string]string{
		"latitude":  "91",
		"longitude": "8.68211",
		"altitude":  "12.5",
		"name":      strings.Repeat("x", 65),
	} {
		got, status := r.set(t, src, stationContext, cfg, map[string]string{name: value})
		if got != nil {
			t.Errorf("%s=%q accepted", name, value)
			continue
		}
		if n := demotest.EventNumber(t, status); n == good {
			t.Errorf("%s=%q: status %d", name, value, n)
		}
	}
}

// Refused Set Config of the application: Values, Min/Max and Pattern.
func TestSetConfigRefused(t *testing.T) {
	t.Parallel()
	r := start(t)
	for name, value := range map[string]string{
		"provider":       "met",
		"stations":       "6",
		"pollInterval":   "0",
		"simulationSeed": "1.5",
		"frostThreshold": "-60",
	} {
		cfg, status := r.set(t, il, appContext, r.config, map[string]string{name: value})
		if cfg != nil {
			t.Errorf("%s=%q accepted", name, value)
			continue
		}
		if n := demotest.EventNumber(t, status); n == good {
			t.Errorf("%s=%q: status %d", name, value, n)
		}
	}
	if r.published(r.at, station(4)) {
		t.Error("a fourth station")
	}
}

// genericEvent waits for a Generic event of the station with the level and
// the Number.
func (r *rig) genericEvent(t *testing.T, from, i int, level string, number uint32) {
	t.Helper()
	topic := r.topic("Pub", "Event", station(i)) + "/Generic/" + level
	r.b.Expect(t, from, fmt.Sprintf("%s event %d of station %d", level, number, i), func(s demotest.Seen) bool {
		if s.Seen != "publish" || s.Topic != topic {
			return false
		}
		n, ok := payload(t, demotest.AsJSON(t, s.Payload))["Number"].(float64)
		return ok && uint32(n) == number
	})
}

// Warnings (A-302): crossing frostThreshold or stormThreshold raises a
// Generic event Frost or Storm of the station (Number 1 begins, 0 ends).
// Thresholds beyond every simulated value make it deterministic. A
// warning may already be on with the defaults (e.g. frost at the
// Zugspitze), so its begin is searched from the start.
func TestWarnings(t *testing.T) {
	t.Parallel()
	r := start(t)
	r.setApp(t, map[string]string{"frostThreshold": "50", "stormThreshold": "0"})
	for i := 1; i <= 3; i++ {
		r.genericEvent(t, r.at, i, "Frost", 1)
		r.genericEvent(t, r.at, i, "Storm", 1)
	}
	from := r.b.Len()
	r.setApp(t, map[string]string{"frostThreshold": "-50", "stormThreshold": "100"})
	for i := 1; i <= 3; i++ {
		r.genericEvent(t, from, i, "Frost", 0)
		r.genericEvent(t, from, i, "Storm", 0)
	}
	r.p.Line(t, 0, "frost line", func(l string) bool { return strings.HasPrefix(l, "event station 1 Frost begins") })
}

// The Config persists: after SIGTERM (exit 0 and the Close message) and a
// restart there are four stations, station 1 keeps its name.
func TestRestartKeepsConfig(t *testing.T) {
	t.Parallel()
	r := start(t)
	r.setApp(t, map[string]string{"stations": "4"})
	cfg, _ := r.publication(t, 0, "Config", station(1), stationContext)
	if next, status := r.set(t, station(1), stationContext, cfg, map[string]string{"name": "Home"}); next == nil {
		t.Fatalf("Set Config refused: %v", status)
	}
	if err := r.p.Stop(t); err != nil {
		t.Fatalf("exit after SIGTERM: %v", err)
	}
	r.b.Expect(t, r.at, "Close message", func(s demotest.Seen) bool {
		return s.Seen == "publish" && s.Topic == r.topic("Pub", "Health", il) && strings.Contains(s.Payload, `"HealthScore":0`)
	})
	from := r.b.Len()
	r.run(t, from)
	if v := configValue(t, r.config, "stations"); v != "4" {
		t.Fatalf("stations after restart %q", v)
	}
	cfg, _ = r.publication(t, from, "Config", station(1), stationContext)
	if v := configValue(t, cfg, "name"); v != "Home" {
		t.Errorf("name after restart %q", v)
	}
	r.publication(t, from, "Data", station(4), "Oi4Pv")
}

// metnoServer is a fake Locationforecast server for the service: Pv is
// 20 + version, Sv1 the latitude rounded (which station asked); a failing
// latitude is answered with 500. Without Expires every poll round asks
// again (conditionally).
type metnoServer struct {
	*fakeMET
	mu      sync.Mutex
	version float64
	failing map[float64]bool
}

func newMetnoServer(t *testing.T) *metnoServer {
	m := &metnoServer{failing: map[float64]bool{}}
	m.fakeMET = newFakeMET(t, func(w http.ResponseWriter, r *http.Request) {
		q, _ := url.ParseQuery(r.URL.RawQuery)
		lat, _ := strconv.ParseFloat(q.Get("lat"), 64)
		m.mu.Lock()
		version, fail := m.version, m.failing[lat] || m.failing[0]
		m.mu.Unlock()
		if fail {
			http.Error(w, "unavailable", http.StatusInternalServerError)
			return
		}
		now := time.Now().UTC().Truncate(time.Hour)
		e := forecastEntry{now, 20 + version, math.Round(lat), 1000, 5, 180}
		next := e
		next.time = now.Add(time.Hour)
		ok(forecast([]forecastEntry{e, next}, nil), time.Time{}, now)(w, r)
	})
	return m
}

// state sets the version of the values and the failing latitudes (0 =
// all).
func (m *metnoServer) state(version float64, failing ...float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.version, m.failing = version, map[float64]bool{}
	for _, lat := range failing {
		m.failing[lat] = true
	}
}

// health waits for a Health of source with the value and score.
func (r *rig) health(t *testing.T, from int, source, health string, score int) {
	t.Helper()
	topic := r.topic("Pub", "Health", source)
	want := fmt.Sprintf(`"Health":"%s","HealthScore":%d`, health, score)
	r.b.Expect(t, from, fmt.Sprintf("Health %s/%d of %s", health, score, source), func(s demotest.Seen) bool {
		return s.Seen == "publish" && s.Topic == topic && strings.Contains(s.Payload, want)
	})
}

// metnoData waits for the Data of station i with the values of the fake
// server of the version.
func (r *rig) metnoData(t *testing.T, from, i int, version float64) int {
	t.Helper()
	topic := r.topic("Pub", "Data", station(i)) + "/Oi4Pv"
	lat := math.Round(defaultLocation(i).Latitude)
	_, at := r.b.Expect(t, from, fmt.Sprintf("Data of station %d from met-no, version %v", i, version), func(s demotest.Seen) bool {
		if s.Seen != "publish" || s.Topic != topic {
			return false
		}
		p := payload(t, demotest.AsJSON(t, s.Payload))
		return p["Pv"] == 20+version && p["Sv1"] == lat && p["Sv2"] == 1000.0 && p["Sv3"] == 5.0 && p["Sv4"] == 180.0
	})
	return at
}

// syslog waits for a Syslog event of the application with the level and
// the text.
func (r *rig) syslog(t *testing.T, from int, level, text string) {
	t.Helper()
	topic := r.topic("Pub", "Event", il) + "/Syslog/" + level
	r.b.Expect(t, from, level+" Syslog event with "+text, func(s demotest.Seen) bool {
		return s.Seen == "publish" && s.Topic == topic && strings.Contains(s.Payload, text)
	})
}

// The provider met-no (ADR-0010) against a fake Locationforecast server
// (OEC_WEATHER_METNO_URL, for tests only): it needs a userAgentContact
// and a poll interval of at least 600 s; switched by Set at run time,
// the stations publish the values of the server. A failing request
// makes the station FAILURE_1/0 without a Data publication and the
// application OFF_SPEC_3 with the share of good stations (Syslog
// Warning); recovery is NORMAL_0/100 again (Syslog Notice).
func TestMetNoProvider(t *testing.T) {
	t.Parallel()
	met := newMetnoServer(t)
	b := demotest.StartBroker(t)
	r := &rig{b: b, root: b.Container(t, host), ui: demotest.UIAddr(t)}
	r.run(t, 0, "OEC_WEATHER_METNO_URL="+met.url())

	for name, values := range map[string]map[string]string{
		"no contact":  {"provider": "met-no", "pollInterval": "600"},
		"60 s":        {"provider": "met-no", "userAgentContact": testContact},
		"bad contact": {"provider": "met-no", "userAgentContact": "a b", "pollInterval": "600"},
		"poll 599 s":  {"provider": "met-no", "userAgentContact": testContact, "pollInterval": "599"},
	} {
		if cfg, status := r.set(t, il, appContext, r.config, values); cfg != nil {
			t.Errorf("%s: %v accepted", name, values)
		} else if n := demotest.EventNumber(t, status); n == good {
			t.Errorf("%s: status %d", name, n)
		}
	}
	if n := len(met.requests()); n != 0 {
		t.Fatalf("%d requests to met-no before it is selected", n)
	}

	from := r.b.Len()
	cfg := r.setApp(t, map[string]string{"provider": "met-no", "userAgentContact": testContact, "pollInterval": "600"})
	if v := configValue(t, cfg, "provider"); v != "met-no" {
		t.Fatalf("provider %q", v)
	}
	for i := 1; i <= 3; i++ {
		r.metnoData(t, from, i, 0)
	}
	// The Web UI credits MET Norway (A-351).
	ui := r.uiState(t, "attribution of met-no", func(s uiStateJSON) bool { return s.Provider == "met-no" && s.Attribution != nil })
	if a := ui.Attribution; a.Text != metnoAttribution || a.License != "CC BY 4.0" ||
		a.LicenseURL != "https://creativecommons.org/licenses/by/4.0/" || !strings.HasPrefix(a.SourceURL, "https://api.met.no/") {
		t.Errorf("attribution %+v", a)
	}
	reqs := met.requests()
	if len(reqs) != 3 {
		t.Fatalf("%d requests", len(reqs))
	}
	for i, q := range reqs {
		if ua := q.header.Get("User-Agent"); ua != "oec-weather/"+version+" "+testContact {
			t.Errorf("User-Agent %q", ua)
		}
		if i > 0 && q.at.Sub(reqs[i-1].at) < 900*time.Millisecond {
			t.Errorf("requests %d and %d %v apart", i-1, i, q.at.Sub(reqs[i-1].at))
		}
	}
	if reqs[0].query != "lat=50.1109&lon=8.6821&altitude=112" {
		t.Errorf("query %q", reqs[0].query)
	}

	// Oslo fails: station 2 FAILURE_1/0 and no Data, the others go on;
	// the application is OFF_SPEC_3 with 2 of 3 good stations.
	met.state(1, defaultLocation(2).Latitude)
	from = r.b.Len()
	r.setApp(t, map[string]string{"frostThreshold": "1"})
	r.health(t, from, station(2), "FAILURE_1", 0)
	r.health(t, from, il, "OFF_SPEC_3", 66)
	r.uiState(t, "station 2 failed", func(s uiStateJSON) bool {
		return s.Health == "OFF_SPEC_3" && s.HealthScore == 66 && s.Stations[1].Failed && s.Stations[1].Health == "FAILURE_1" &&
			s.Stations[1].Observation == nil && s.Stations[1].Error != "" && !s.Stations[0].Failed
	})
	r.syslog(t, from, "Warning", "station 2 Oslo")
	r.metnoData(t, from, 1, 1)
	r.metnoData(t, from, 3, 1)
	r.p.Line(t, 0, "failure line", func(l string) bool { return strings.HasPrefix(l, "station 2 Oslo failed: ") })

	// Recovery.
	met.state(2)
	from2 := r.b.Len()
	r.setApp(t, map[string]string{"frostThreshold": "0"})
	r.health(t, from2, station(2), "NORMAL_0", 100)
	r.health(t, from2, il, "NORMAL_0", 100)
	r.syslog(t, from2, "Notice", "station 2 Oslo")
	at := r.metnoData(t, from2, 2, 2)
	for _, s := range r.b.Events()[from:at] {
		if s.Seen == "publish" && strings.HasPrefix(s.Topic, r.topic("Pub", "Data", station(2))) {
			t.Errorf("Data of the failed station 2: %s", s.Payload)
		}
	}

	// Every request fails: OFF_SPEC_3 with HealthScore 0 (never
	// FAILURE_1/0 about itself, REQ-LIFE-012/017).
	met.state(3, 0)
	from = r.b.Len()
	r.setApp(t, map[string]string{"frostThreshold": "1"})
	for i := 1; i <= 3; i++ {
		r.health(t, from, station(i), "FAILURE_1", 0)
	}
	r.health(t, from, il, "OFF_SPEC_3", 0)

	// Back to the simulation: every station NORMAL again, no further
	// request.
	from = r.b.Len()
	r.setApp(t, map[string]string{"provider": "simulation"})
	for i := 1; i <= 3; i++ {
		r.health(t, from, station(i), "NORMAL_0", 100)
		r.publication(t, from, "Data", station(i), "Oi4Pv")
	}
	r.health(t, from, il, "NORMAL_0", 100)
	r.uiState(t, "no attribution with the simulation", func(s uiStateJSON) bool { return s.Provider == "simulation" && s.Attribution == nil })
	n, pl := len(met.requests()), r.p.Len()
	r.setApp(t, map[string]string{"frostThreshold": "0"})
	r.p.Line(t, pl, "poll line", func(l string) bool { return strings.HasPrefix(l, "poll 3 stations") })
	if len(met.requests()) != n {
		t.Errorf("requests to met-no with the simulation")
	}
	for _, s := range r.b.Events() {
		if s.Seen == "publish" && s.Topic == r.topic("Pub", "Health", il) && strings.Contains(s.Payload, `"FAILURE_1"`) {
			t.Errorf("FAILURE_1 about itself: %s", s.Payload)
		}
	}
}

// D7 `oec-check test --level 3` against the weather service (ADR-0028 §6):
// the lifecycle, Get of every resource, Set of every resource of the
// application and of a represented station (REQ-CMP-033).
func TestD7Level3(t *testing.T) {
	demotest.RunD7(t, demotest.D7{
		Exe:      exe(t),
		Hostname: "app-1",
		App:      "OTConnector/oi4.org/OEC,20Weather/OEC-WEATHER/app-1",
		Level:    3,
		Pass:     d7Level3Pass,
		Report:   "OEC_WEATHER_D7_REPORT",
		Env:      []string{"OEC_UI_LISTEN="},
	})
}

// d7Level3Pass: the 49 D7 tests that pass against the weather service
// (C-080), as against the Go SDK example source. D7 skips the other six:
// T7-MON-07, T7-MON-17, T7-ADR-01 and T7-ADR-02 judge requests of the CUT
// (the weather service sends none); T7-SET-04 and T7-SET-10 need a
// ReferenceDesignation object, which the fresh store of the run has not.
var d7Level3Pass = []string{
	"T7-MON-01", "T7-MON-02", "T7-MON-03", "T7-MON-04", "T7-MON-05", "T7-MON-06", "T7-MON-08", "T7-MON-09", "T7-MON-10",
	"T7-MON-11", "T7-MON-12", "T7-MON-13", "T7-MON-14", "T7-MON-15", "T7-MON-16", "T7-MON-18", "T7-MON-19",
	"T7-CON-01", "T7-CON-02", "T7-CON-03", "T7-CON-04",
	"T7-LIFE-01", "T7-LIFE-02", "T7-LIFE-03", "T7-LIFE-04",
	"T7-ADR-03",
	"T7-GET-01", "T7-GET-02", "T7-GET-03", "T7-GET-04", "T7-GET-05", "T7-GET-06", "T7-GET-07", "T7-GET-08", "T7-GET-09",
	"T7-SET-01", "T7-SET-02", "T7-SET-03", "T7-SET-05", "T7-SET-06", "T7-SET-07", "T7-SET-08", "T7-SET-09", "T7-SET-11", "T7-SET-12",
	"T7-ACT-01", "T7-ACT-02", "T7-ACT-03", "T7-ACT-04",
}
