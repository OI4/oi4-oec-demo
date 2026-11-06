//go:build unix

// Integration tests of the config service D10 (ADR-0028 §6, A-304): the
// binary oec-config against the in-process TLS test broker of the Rust SDK,
// offline, with the logger (D4) and the weather service (D3) as the
// applications it configures. The gate runs them against the debug build
// (oec/demos/check.sh).
package config

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/OI4/oec/demos/internal/demotest"
)

// Every application of a test has its own hostname: it is the MQTT client
// id of the SDK.
const (
	host        = "config-1"
	loggerHost  = "logger-1"
	weatherHost = "weather-1"
)

// The Identification Links (REQ-IL-002).
var (
	il        = "oi4.org/OEC,20Config/OEC-CONFIG/" + host
	loggerIL  = "oi4.org/OEC,20Logger/OEC-LOGGER/" + loggerHost
	weatherIL = "oi4.org/OEC,20Weather/OEC-WEATHER/" + weatherHost
)

const (
	loggerContext  = "logger"
	weatherContext = "weather"
	// BadOutOfRange (MG 8.3.6): the weather service refuses met-no with a
	// pollInterval below 600 s (A-348).
	badOutOfRange = 0x803C0000
	// A contact of the weather tests, never contacted (offline).
	contact = "oec-weather@example.invalid"
)

func exe(t *testing.T) string {
	return demotest.Tool(t, "OEC_CONFIG", "demos/config/target/debug/oec-config", "cargo build in oec/demos/config")
}

// start runs an application on the broker b with its own container storage
// and waits for its Pub Config of the context; its Web UI is off (the
// default ports would clash).
func start(t *testing.T, b *demotest.Broker, env, def, hint, hostname, serviceType, app, context string) *demotest.Process {
	t.Helper()
	path := demotest.Tool(t, env, def, hint)
	from := b.Len()
	p := demotest.Start(t, path, "OEC_ROOT="+b.Container(t, hostname), "OEC_UI_LISTEN=")
	topic := demotest.Topic(app)
	b.Publish(t, from, "Oi4v2/"+serviceType+"/"+topic+"/Pub/Config/"+topic+"/"+context)
	return p
}

func startLogger(t *testing.T, b *demotest.Broker) *demotest.Process {
	t.Helper()
	return start(t, b, "OEC_LOGGER", "demos/logger/target/oec-logger", "make in oec/demos/logger",
		loggerHost, "Utility", loggerIL, loggerContext)
}

func startWeather(t *testing.T, b *demotest.Broker) *demotest.Process {
	t.Helper()
	return start(t, b, "OEC_WEATHER", "demos/weather/target/oec-weather", "go build -o weather/target/oec-weather ./weather in oec/demos",
		weatherHost, "OTConnector", weatherIL, weatherContext)
}

// service is a running config service and its HTTP API.
type service struct {
	p    *demotest.Process
	addr string
}

// startService runs the config service on b with the settings file
// (none if "") and the environment env and waits for its HTTP API.
func startService(t *testing.T, b *demotest.Broker, settings string, env ...string) *service {
	t.Helper()
	root := b.Container(t, host)
	if settings != "" {
		demotest.Write(t, filepath.Join(root, "etc", "oi4", "app", "config-service.json"), settings)
	}
	s := &service{addr: fmt.Sprintf("127.0.0.1:%d", demotest.FreePort(t))}
	s.p = demotest.Start(t, exe(t), append([]string{"OEC_ROOT=" + root, "OEC_UI_LISTEN=" + s.addr, "OEC_CONFIG_LOG=debug"}, env...)...)
	demotest.Eventually(t, "HTTP API on "+s.addr, func() bool {
		c, err := net.Dial("tcp", s.addr)
		if err == nil {
			c.Close()
		}
		return err == nil
	})
	return s
}

// do sends a request to the API; body nil = none.
func (s *service) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, "http://"+s.addr+path, r)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s %s: %d %q: %v", method, path, resp.StatusCode, raw, err)
	}
	return resp.StatusCode, out
}

// app is the entry of the application in GET /api/apps, nil if unknown.
func (s *service) app(t *testing.T, serviceType, app string) map[string]any {
	t.Helper()
	_, out := s.do(t, "GET", "/api/apps", nil)
	apps, _ := out["apps"].([]any)
	for _, a := range apps {
		m, _ := a.(map[string]any)
		if m["serviceType"] == serviceType && m["app"] == app {
			return m
		}
	}
	return nil
}

// config is the Config entry of the context of the application itself.
func config(app map[string]any, source, context string) map[string]any {
	configs, _ := app["configs"].([]any)
	for _, c := range configs {
		m, _ := c.(map[string]any)
		if m["source"] == source && m["context"] == context {
			return m
		}
	}
	return nil
}

// value is the Value of the object of group common of a Config entry.
func value(entry map[string]any, object string) string {
	cfg, _ := entry["config"].(map[string]any)
	common, _ := cfg["common"].(map[string]any)
	obj, _ := common[object].(map[string]any)
	v, _ := obj["Value"].(string)
	return v
}

// writable waits until the service lists the Config of the context of the
// application itself as writable and returns its entry.
func (s *service) writable(t *testing.T, serviceType, app, context string) map[string]any {
	t.Helper()
	var entry map[string]any
	demotest.Eventually(t, "writable Config "+context+" of "+app, func() bool {
		entry = config(s.app(t, serviceType, app), app, context)
		return entry != nil && entry["writable"] == true
	})
	return entry
}

func setBody(serviceType, app, context string, values map[string]string) map[string]any {
	return map[string]any{
		"serviceType": serviceType, "app": app, "source": app, "context": context,
		"values": map[string]any{"common": values},
	}
}

func status(t *testing.T, record map[string]any) (uint32, string, bool) {
	t.Helper()
	st, ok := record["status"].(map[string]any)
	if !ok {
		t.Fatalf("no Status event: %v", record)
	}
	code, _ := st["code"].(float64)
	symbol, _ := st["symbol"].(string)
	good, _ := st["good"].(bool)
	return uint32(code), symbol, good
}

// The service finds the logger by Get MAM at its start (configured) and
// the weather service by its Birth, reads their Config and
// SubscriptionList (SF-107), sets a logger value (Good, at the target and
// read again) and reports a refused Set of the weather service; offline
// applications are not set.
func TestDiscoverReadSet(t *testing.T) {
	t.Parallel()
	b := demotest.StartBroker(t)
	logger := startLogger(t, b)
	s := startService(t, b, `{"apps": ["Utility/`+loggerIL+`"], "requestTimeoutSeconds": 5}`)
	connect, _ := b.Expect(t, 0, "CONNECT of the config service", func(x demotest.Seen) bool {
		return x.Seen == "connect" && x.ClientID == host
	})

	entry := s.writable(t, "Utility", loggerIL, loggerContext)
	if v := value(entry, "minEventLevel"); v != "Informational" {
		t.Errorf("logger minEventLevel %q", v)
	}
	app := s.app(t, "Utility", loggerIL)
	if app["state"] != "online" || app["mam"] == nil || app["subscriptionList"] != true {
		t.Errorf("logger %v", app)
	}
	if profile, _ := app["profile"].([]any); !slices.Contains(profile, any("Config")) {
		t.Errorf("logger Profile %v", app["profile"])
	}
	if s.app(t, "Utility", il) != nil {
		t.Error("the config service lists itself")
	}

	startWeather(t, b)
	entry = s.writable(t, "OTConnector", weatherIL, weatherContext)
	if v := value(entry, "provider"); v != "simulation" {
		t.Errorf("weather provider %q", v)
	}

	// Set: the Set Config on the bus, the answer of the logger, the cache.
	from := b.Len()
	code, rec := s.do(t, "POST", "/api/set", setBody("Utility", loggerIL, loggerContext, map[string]string{"minEventLevel": "Debug"}))
	if code != 200 {
		t.Fatalf("set: %d %v", code, rec)
	}
	if n, sym, good := status(t, rec); n != 0 || sym != "Good" || !good {
		t.Errorf("logger Set: %d %s %v", n, sym, good)
	}
	topic := demotest.Topic(loggerIL)
	set, _ := b.Expect(t, from, "Set Config of the config service", func(x demotest.Seen) bool {
		return x.Seen == "publish" && x.Conn == connect.Conn &&
			x.Topic == "Oi4v2/Utility/"+topic+"/Set/Config/"+topic+"/"+loggerContext
	})
	if !strings.Contains(set.Payload, `"minEventLevel":{"Value":"Debug"}`) {
		t.Errorf("Set payload %s", set.Payload)
	}
	b.Expect(t, from, "Pub Config of the logger with Debug", func(x demotest.Seen) bool {
		return x.Seen == "publish" && strings.HasPrefix(x.Topic, "Oi4v2/Utility/"+topic+"/Pub/Config/") &&
			strings.Contains(x.Payload, `"Debug"`)
	})
	if v := value(config(s.app(t, "Utility", loggerIL), loggerIL, loggerContext), "minEventLevel"); v != "Debug" {
		t.Errorf("after the Set: minEventLevel %q", v)
	}

	// A Set the target refuses: met-no needs a pollInterval of 600 s.
	code, rec = s.do(t, "POST", "/api/set", setBody("OTConnector", weatherIL, weatherContext,
		map[string]string{"provider": "met-no", "userAgentContact": contact, "pollInterval": "599"}))
	if code != 200 {
		t.Fatalf("set: %d %v", code, rec)
	}
	if n, sym, good := status(t, rec); n != badOutOfRange || sym != "BadOutOfRange" || good {
		t.Errorf("weather Set: 0x%08X %s %v", n, sym, good)
	}
	if v := value(config(s.app(t, "OTConnector", weatherIL), weatherIL, weatherContext), "provider"); v != "simulation" {
		t.Errorf("after the refused Set: provider %q", v)
	}
	_, sets := s.do(t, "GET", "/api/sets", nil)
	list, _ := sets["sets"].([]any)
	if len(list) != 2 {
		t.Fatalf("sets %v", sets)
	}
	if first, _ := list[0].(map[string]any); first["app"] != weatherIL {
		t.Errorf("newest first: %v", list)
	}

	// Checked before sending.
	for name, c := range map[string]struct {
		body map[string]any
		code int
	}{
		"unknown object":  {setBody("Utility", loggerIL, loggerContext, map[string]string{"nope": "1"}), 400},
		"unknown context": {setBody("Utility", loggerIL, "nope", map[string]string{"format": "text"}), 404},
		"unknown app":     {setBody("Utility", "oi4.org/X/Y/z", loggerContext, map[string]string{"format": "text"}), 404},
	} {
		if code, out := s.do(t, "POST", "/api/set", c.body); code != c.code {
			t.Errorf("%s: %d %v", name, code, out)
		}
	}
	if code, out := s.do(t, "POST", "/api/refresh", map[string]any{}); code != 200 {
		t.Errorf("refresh: %d %v", code, out)
	}

	// Offline (Close): listed, not set.
	if err := logger.Stop(t); err != nil {
		t.Fatal(err)
	}
	demotest.Eventually(t, "logger offline", func() bool {
		return s.app(t, "Utility", loggerIL)["state"] == "offlineGraceful"
	})
	if code, out := s.do(t, "POST", "/api/set", setBody("Utility", loggerIL, loggerContext, map[string]string{"format": "text"})); code != 409 {
		t.Errorf("offline: %d %v", code, out)
	}
}

// raw sends a request as text and returns the status line.
func raw(t *testing.T, addr, request string) string {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := io.WriteString(c, request); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(line)
}

// The HTTP API without authentication (A-305) refuses other hosts and
// sites (DNS rebinding, CSRF) and requests while offline.
func TestAPIGuards(t *testing.T) {
	t.Parallel()
	b := demotest.StartBroker(t)
	s := startService(t, b, "")
	for request, want := range map[string]string{
		"GET /api/apps HTTP/1.1\r\nHost: " + s.addr + "\r\n\r\n":                                                        "HTTP/1.1 200 OK",
		"GET /api/apps HTTP/1.1\r\nHost: config.example\r\n\r\n":                                                        "HTTP/1.1 403 Forbidden",
		"GET /api/apps HTTP/1.1\r\nHost: " + s.addr + "\r\nOrigin: http://config.example\r\n\r\n":                       "HTTP/1.1 403 Forbidden",
		"POST /api/refresh HTTP/1.1\r\nHost: " + s.addr + "\r\nContent-Type: text/plain\r\nContent-Length: 2\r\n\r\n{}": "HTTP/1.1 415 Unsupported Media Type",
		"DELETE /api/set HTTP/1.1\r\nHost: " + s.addr + "\r\n\r\n":                                                      "HTTP/1.1 405 Method Not Allowed",
	} {
		if got := raw(t, s.addr, request); got != want {
			t.Errorf("%q: %q, want %q", request, got, want)
		}
	}
	b.Expect(t, 0, "CONNECT of the config service", func(x demotest.Seen) bool {
		return x.Seen == "connect" && x.ClientID == host
	})
	if code, out := s.do(t, "GET", "/api/apps", nil); code != 200 || out["apps"] == nil {
		t.Errorf("apps: %d %v", code, out)
	}
}

// An invalid settings file stops the service with a message.
func TestInvalidSettings(t *testing.T) {
	t.Parallel()
	b := demotest.StartBroker(t)
	root := b.Container(t, host)
	demotest.Write(t, filepath.Join(root, "etc", "oi4", "app", "config-service.json"), `{"listn": "127.0.0.1:1"}`)
	p := demotest.Start(t, exe(t), "OEC_ROOT="+root)
	demotest.Eventually(t, "the message", func() bool { return strings.Contains(p.Stderr(), `unknown key "listn"`) })
	if err := p.Stop(t); err == nil {
		t.Error("exit 0")
	}
}

func TestD7Level1(t *testing.T) {
	demotest.RunD7(t, demotest.D7{
		Exe:      exe(t),
		Env:      []string{"OEC_UI_LISTEN="},
		Hostname: "app-1",
		App:      "Utility/oi4.org/OEC,20Config/OEC-CONFIG/app-1",
		Level:    1,
		Pass:     d7Level1Pass,
		Report:   "OEC_CONFIG_D7_REPORT",
	})
}

// d7Level1Pass: the 22 D7 tests that pass against the config service
// (C-082). The service discovers the D7 peer and sends it Get requests,
// which T7-ADR-01 and T7-ADR-03 judge. A Level 1 application answers no
// Get, Set or Call (REQ-CMP-017 applies from Level 2): D7 warns on five
// tests for the missing SHOULD answers (T7-GET-05, T7-SET-01, T7-SET-02,
// T7-SET-06, T7-SET-07) and skips the other 28 – the Level 2 and 3 tests
// (T7-MON-13, T7-SET-03), the request tests without an answer to judge
// (T7-GET-01 … 04, 06 … 09, T7-SET-04, 05, 08 … 12, T7-ACT-*), and the
// monitor tests whose trigger the run does not contain – no Data, Metadata or cyclic
// publications (T7-MON-09, T7-MON-14), no change of the lists (T7-MON-15),
// no paged answer (T7-MON-18), no two pending requests with one RequestId
// (T7-MON-07), no Set Config to the peer (T7-MON-17), no second version
// (T7-ADR-02).
var d7Level1Pass = []string{
	"T7-MON-01", "T7-MON-02", "T7-MON-03", "T7-MON-04", "T7-MON-05", "T7-MON-06", "T7-MON-08", "T7-MON-10", "T7-MON-11", "T7-MON-12", "T7-MON-16", "T7-MON-19",
	"T7-CON-01", "T7-CON-02", "T7-CON-03", "T7-CON-04",
	"T7-LIFE-01", "T7-LIFE-02", "T7-LIFE-03", "T7-LIFE-04",
	"T7-ADR-01", "T7-ADR-03",
}
