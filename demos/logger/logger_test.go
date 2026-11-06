//go:build unix

// Integration tests of the logger demo D4 (ADR-0028 §6): the binary
// against the in-process TLS test broker of the Rust SDK, offline. The
// gate runs them against the ASan/UBSan build (oec/demos/check.sh).
package logger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/OI4/oec/demos/internal/demotest"
)

const host = "logger-1"

// The Identification Link of the logger (REQ-IL-002) and its topic level.
var (
	il = "oi4.org/OEC,20Logger/OEC-LOGGER/" + host
	me = demotest.Topic(il)
)

const (
	pump  = "vendor.com/Pump/P-3/0815"
	valve = "vendor.com/Valve/V-1/4711"
)

const (
	// Status codes (MG 8.3.6).
	good          = 0
	badOutOfRange = 2151415808

	configContext        = "logger"
	recordResourceHealth = "Health"
)

func exe(t *testing.T) string {
	return demotest.Tool(t, "OEC_LOGGER", "demos/logger/target/oec-logger", "make in oec/demos/logger")
}

// rig is a logger on its own test broker.
type rig struct {
	b    *demotest.Broker
	root string
	p    *demotest.Process
	conn int
	// The first Pub Config and the index of the broker events after it.
	config map[string]any
	at     int
	// The address of the Web UI (OEC_UI_LISTEN).
	ui string
}

// start runs a logger with the extra environment env.
func start(t *testing.T, env ...string) *rig {
	t.Helper()
	b := demotest.StartBroker(t)
	r := &rig{b: b, root: b.Container(t, host), ui: demotest.UIAddr(t)}
	r.run(t, 0, env...)
	return r
}

// run starts the logger (again) with its Web UI on r.ui and waits for its
// Pub Config.
func (r *rig) run(t *testing.T, from int, env ...string) {
	t.Helper()
	r.p = demotest.Start(t, exe(t), append([]string{"OEC_ROOT=" + r.root, "OEC_UI_LISTEN=" + r.ui}, env...)...)
	connect, i := r.b.Expect(t, from, "CONNECT of the logger", func(s demotest.Seen) bool {
		return s.Seen == "connect" && s.ClientID == host
	})
	r.conn = connect.Conn
	pub, j := r.b.Publish(t, i, r.topic("Pub", "Config")+"/"+configContext)
	r.config = demotest.AsJSON(t, pub.Payload)
	r.at = j
}

func (r *rig) topic(method, resource string) string {
	return "Oi4v2/Utility/" + me + "/" + method + "/" + resource + "/" + me
}

// record is one JSON record of the logger.
type record struct {
	Time        string          `json:"time"`
	Resource    string          `json:"resource"`
	ServiceType string          `json:"serviceType"`
	App         string          `json:"app"`
	Source      *string         `json:"source"`
	Filter      *string         `json:"filter"`
	Level       string          `json:"level"`
	Payload     json.RawMessage `json:"payload"`
}

func parse(line string) (record, bool) {
	var rec record
	return rec, json.Unmarshal([]byte(line), &rec) == nil
}

// recordLine waits for a JSON record with the resource and the AppId whose
// payload contains part.
func (r *rig) recordLine(t *testing.T, from int, resource, app, part string) (record, int) {
	t.Helper()
	var rec record
	_, i := r.p.Line(t, from, resource+" record of "+app+" with "+part, func(line string) bool {
		x, ok := parse(line)
		if ok && x.Resource == resource && x.App == app && strings.Contains(string(x.Payload), part) {
			rec = x
			return true
		}
		return false
	})
	return rec, i
}

// inject sends a self-initiated publication of another application to the
// logger: the test broker delivers it on the logger's connection.
func (r *rig) inject(t *testing.T, serviceType, app, resource, source, filter string, payload any) {
	t.Helper()
	topic, msg := demotest.Pub(serviceType, app, resource, source, filter, payload)
	r.b.Send(t, r.conn, topic, msg)
}

func syslog(pri int, msg string) map[string]any {
	return map[string]any{"Number": pri, "Category": "CAT_SYSLOG_0", "Details": map[string]any{"MSG": msg, "HEADER": ""}}
}

// set sends a Set Config with the values and returns the answering Pub
// Config (nil if the Set is refused) and the Status event.
func (r *rig) set(t *testing.T, values map[string]string) (map[string]any, map[string]any) {
	t.Helper()
	common := map[string]any{}
	for k, v := range values {
		common[k] = map[string]any{"Value": v}
	}
	from := r.b.Len()
	id := r.b.Request(t, r.conn, r.topic("Set", "Config")+"/"+configContext, r.config, map[string]any{"common": common})
	ev, _ := r.b.Expect(t, from, "Status event answering "+id, func(s demotest.Seen) bool {
		return s.Seen == "publish" && strings.HasPrefix(s.Topic, r.topic("Pub", "Event")+"/Status/") &&
			strings.Contains(s.Payload, `"ReplyTo":"`+id+`"`)
	})
	status := demotest.AsJSON(t, ev.Payload)
	if demotest.EventNumber(t, status) != good {
		return nil, status
	}
	cfg, _ := r.b.Answer(t, from, r.topic("Pub", "Config")+"/"+configContext, id)
	return cfg, status
}

func configValue(t *testing.T, cfg map[string]any, name string) string {
	t.Helper()
	common := cfg["Messages"].([]any)[0].(map[string]any)["Payload"].(map[string]any)["common"].(map[string]any)
	v, _ := common[name].(map[string]any)["Value"].(string)
	return v
}

// filters are the topic filters the logger subscribed to.
func (r *rig) filters() []string {
	var all []string
	for _, s := range r.b.Events() {
		if s.Conn != r.conn {
			continue
		}
		switch s.Seen {
		case "subscribe":
			all = append(all, s.Filters...)
		case "unsubscribe":
			all = slices.DeleteFunc(all, func(f string) bool { return slices.Contains(s.Filters, f) })
		}
	}
	return all
}

func subscribedTo(filters []string, resource string) bool {
	return slices.ContainsFunc(filters, func(f string) bool { return strings.Contains(f, "/Pub/"+resource+"/") })
}

// The defaults: Event and Health of every application subscribed, Data
// not; the Config published with its defaults (A-303); the own Health
// logged as a JSON record (the broker routes it back).
func TestDefaults(t *testing.T) {
	t.Parallel()
	r := start(t)
	for name, want := range map[string]string{
		"event": "true", "health": "true", "data": "false", "sources": "", "minEventLevel": "Informational",
		"format": "json", "file": "", "historySize": "1000",
	} {
		if v := configValue(t, r.config, name); v != want {
			t.Errorf("Config %s = %q, want %q", name, v, want)
		}
	}
	demotest.Eventually(t, "Event and Health subscribed", func() bool {
		return subscribedTo(r.filters(), "Event") && subscribedTo(r.filters(), "Health")
	})
	// The answer to a Get Health is a publication the logger subscribed to
	// as well (the Birth Health may come before the subscription).
	from := r.p.Len()
	r.b.Get(t, r.conn, r.topic("Get", "Health"), "Health")
	rec, _ := r.recordLine(t, from, recordResourceHealth, il, `"NORMAL_0"`)
	if rec.ServiceType != "Utility" || rec.Source == nil || *rec.Source != il || rec.Filter != nil || rec.Level != "" {
		t.Errorf("own Health record %+v", rec)
	}
	if subscribedTo(r.filters(), "Data") {
		t.Errorf("filters %v", r.filters())
	}
}

// Events and Health of other applications: the Event level of Syslog
// (PRI % 8) and Status events, the default minimum Informational; Data is
// not subscribed by default.
func TestOtherApplications(t *testing.T) {
	t.Parallel()
	r := start(t)
	r.inject(t, "OTConnector", pump, "Event", pump, "Syslog/Notice", syslog(13, "pump notice"))
	r.inject(t, "OTConnector", pump, "Event", pump, "Syslog/Debug", syslog(15, "pump debug"))
	r.inject(t, "OTConnector", pump, "Health", pump, "", map[string]any{"Health": "MAINTENANCE_REQUIRED_4", "HealthScore": 70})
	r.inject(t, "OTConnector", pump, "Data", pump, "flow", map[string]any{"Flow": 12.5})
	r.inject(t, "OTConnector", pump, "Event", pump, "Status/Bad",
		map[string]any{"Number": badOutOfRange, "Category": "CAT_STATUS_1", "Description": "marker", "Details": map[string]any{"SymbolicId": "BadOutOfRange"}})

	notice, i := r.recordLine(t, 0, "Event", pump, "pump notice")
	if notice.Level != "Notice" || notice.ServiceType != "OTConnector" || notice.Filter == nil || *notice.Filter != "Syslog/Notice" {
		t.Errorf("Syslog record %+v", notice)
	}
	health, _ := r.recordLine(t, i, recordResourceHealth, pump, "MAINTENANCE_REQUIRED_4")
	if health.Source == nil || *health.Source != pump {
		t.Errorf("Health record %+v", health)
	}
	status, _ := r.recordLine(t, i, "Event", pump, "marker")
	if status.Level != "Error" {
		t.Errorf("Status Bad record %+v", status)
	}
	for _, line := range r.p.Lines() {
		if strings.Contains(line, "pump debug") || strings.Contains(line, `"Flow"`) {
			t.Errorf("logged: %s", line)
		}
	}
}

// Set Config (Messaging Level 2, REQ-RES-CONFIG-*): every object applied
// at once – Data subscribed, the source filter, the minimum level, the
// text format and the file in /opt/oi4/app.
func TestSetConfig(t *testing.T) {
	t.Parallel()
	r := start(t)
	cfg, status := r.set(t, map[string]string{
		"data": "true", "sources": "vendor.com/Pump", "minEventLevel": "Debug", "format": "text", "file": "pump.log",
	})
	if cfg == nil {
		t.Fatalf("Set Config refused: %v", status)
	}
	if v := configValue(t, cfg, "format"); v != "text" {
		t.Errorf("format %q", v)
	}
	demotest.Eventually(t, "Data subscribed", func() bool { return subscribedTo(r.filters(), "Data") })

	from := r.p.Len()
	r.inject(t, "OTConnector", valve, "Data", valve, "flow", map[string]any{"Flow": 1})
	r.inject(t, "OTConnector", pump, "Data", pump, "flow", map[string]any{"Flow": 12.5})
	r.inject(t, "OTConnector", pump, "Event", pump, "Syslog/Debug", syslog(15, "pump debug"))
	line, _ := r.p.Line(t, from, "text record of the Syslog Debug event", func(l string) bool {
		return strings.Contains(l, "pump debug")
	})
	if !strings.Contains(line, " Event Debug OTConnector "+pump+" "+pump+" Syslog/Debug {") {
		t.Errorf("text record %q", line)
	}
	r.p.Line(t, from, "text record of the pump Data", func(l string) bool {
		return strings.Contains(l, " Data - OTConnector "+pump+" "+pump+" flow ") && strings.Contains(l, "12.5")
	})
	for _, l := range r.p.Lines()[from:] {
		if strings.Contains(l, valve) || strings.HasPrefix(l, "{") {
			t.Errorf("logged: %s", l)
		}
	}
	path := filepath.Join(r.root, "opt", "oi4", "app", "pump.log")
	demotest.Eventually(t, "records in "+path, func() bool {
		b, _ := os.ReadFile(path)
		return strings.Contains(string(b), "pump debug") && strings.Contains(string(b), "12.5")
	})

	// Back to the defaults: "" unsets every value (REQ-RES-CONFIG-007).
	cfg, status = r.set(t, map[string]string{
		"data": "", "sources": "", "minEventLevel": "", "format": "", "file": "",
	})
	if cfg == nil {
		t.Fatalf("Set Config refused: %v", status)
	}
	demotest.Eventually(t, "Data unsubscribed", func() bool { return !subscribedTo(r.filters(), "Data") })
	from = r.p.Len()
	r.inject(t, "OTConnector", valve, "Health", valve, "", map[string]any{"Health": "NORMAL_0", "HealthScore": 100})
	r.recordLine(t, from, recordResourceHealth, valve, "NORMAL_0")
}

// Refused Set Config: the checks of the SDK (Min, Pattern, Values) answer
// with Status Bad, nothing changes.
func TestSetConfigRefused(t *testing.T) {
	t.Parallel()
	r := start(t)
	for name, value := range map[string]string{
		"historySize":   "5",
		"file":          "../escape.log",
		"format":        "xml",
		"minEventLevel": "Loud",
	} {
		cfg, status := r.set(t, map[string]string{name: value})
		if cfg != nil {
			t.Errorf("%s=%q accepted", name, value)
			continue
		}
		if n := demotest.EventNumber(t, status); n == good {
			t.Errorf("%s=%q: status %d", name, value, n)
		}
	}
	if _, err := os.Stat(filepath.Join(r.root, "opt", "oi4", "escape.log")); err == nil {
		t.Error("escape.log written")
	}
	// Still the defaults: Health is logged as JSON.
	from := r.p.Len()
	r.inject(t, "OTConnector", pump, "Health", pump, "", map[string]any{"Health": "NORMAL_0", "HealthScore": 100})
	r.recordLine(t, from, recordResourceHealth, pump, `"NORMAL_0"`)
}

// The Config persists (REQ-RES-CONFIG-*): after SIGTERM (exit 0 and the
// Close message) and a restart the logger writes text.
func TestRestartKeepsConfig(t *testing.T) {
	t.Parallel()
	r := start(t)
	if cfg, status := r.set(t, map[string]string{"format": "text", "health": "false"}); cfg == nil {
		t.Fatalf("Set Config refused: %v", status)
	}
	demotest.Eventually(t, "Health unsubscribed", func() bool { return !subscribedTo(r.filters(), "Health") })
	if err := r.p.Stop(t); err != nil {
		t.Fatalf("exit after SIGTERM: %v", err)
	}
	r.b.Expect(t, r.at, "Close message", func(s demotest.Seen) bool {
		return s.Seen == "publish" && strings.HasPrefix(s.Topic, r.topic("Pub", "Health")) && strings.Contains(s.Payload, `"HealthScore":0`)
	})
	r.run(t, r.b.Len())
	if v := configValue(t, r.config, "format"); v != "text" {
		t.Fatalf("format after restart %q", v)
	}
	if subscribedTo(r.filters(), "Health") || !subscribedTo(r.filters(), "Event") {
		t.Errorf("filters after restart %v", r.filters())
	}
	from := r.p.Len()
	r.inject(t, "OTConnector", pump, "Event", pump, "Syslog/Notice", syslog(13, "after restart"))
	line, _ := r.p.Line(t, from, "text record", func(l string) bool { return strings.Contains(l, "after restart") })
	if strings.HasPrefix(line, "{") {
		t.Errorf("JSON after restart: %s", line)
	}
}

// D7 `oec-check test --level 2` against the logger (ADR-0028 §6): the
// lifecycle, Get of every resource of Level 2 and Set Config. The other
// tests are skipped by D7 (requests of the CUT, Level 3).
func TestD7Level2(t *testing.T) {
	demotest.RunD7(t, demotest.D7{
		Exe:      exe(t),
		Env:      []string{"OEC_UI_LISTEN="},
		Hostname: "app-1",
		App:      "Utility/oi4.org/OEC,20Logger/OEC-LOGGER/app-1",
		Level:    2,
		Pass:     d7Level2Pass,
		Report:   "OEC_LOGGER_D7_REPORT",
	})
}

// d7Level2Pass: the 46 D7 tests that pass against the logger (C-079). D7
// skips the other nine: T7-MON-07, T7-MON-17, T7-ADR-01 and T7-ADR-02 judge
// requests of the CUT (none sent); T7-MON-09 and T7-MON-14 judge Data,
// Metadata and cyclic publications (the logger has none); T7-SET-04 and
// T7-SET-10 need a ReferenceDesignation object, which the fresh store of
// the run does not have; T7-SET-03 is Level 3.
var d7Level2Pass = []string{
	"T7-MON-01", "T7-MON-02", "T7-MON-03", "T7-MON-04", "T7-MON-05", "T7-MON-06", "T7-MON-08", "T7-MON-10", "T7-MON-11", "T7-MON-12", "T7-MON-13", "T7-MON-15", "T7-MON-16", "T7-MON-18", "T7-MON-19",
	"T7-CON-01", "T7-CON-02", "T7-CON-03", "T7-CON-04",
	"T7-LIFE-01", "T7-LIFE-02", "T7-LIFE-03", "T7-LIFE-04",
	"T7-ADR-03",
	"T7-GET-01", "T7-GET-02", "T7-GET-03", "T7-GET-04", "T7-GET-05", "T7-GET-06", "T7-GET-07", "T7-GET-08", "T7-GET-09",
	"T7-SET-01", "T7-SET-02", "T7-SET-05", "T7-SET-06", "T7-SET-07", "T7-SET-08", "T7-SET-09", "T7-SET-11", "T7-SET-12",
	"T7-ACT-01", "T7-ACT-02", "T7-ACT-03", "T7-ACT-04",
}
