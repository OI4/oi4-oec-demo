//go:build unix

package config

import (
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/OI4/oec/demos/internal/demotest"
)

// appsEntry is the entry of the application in an event "apps" (the same
// as GET /api/apps), nil if not listed.
func appsEntry(d map[string]any, serviceType, app string) map[string]any {
	apps, _ := d["apps"].([]any)
	for _, a := range apps {
		m, _ := a.(map[string]any)
		if m["serviceType"] == serviceType && m["app"] == app {
			return m
		}
	}
	return nil
}

func setRecord(d map[string]any) map[string]any {
	r, _ := d["set"].(map[string]any)
	return r
}

// The Web UI (ADR-0028 §3, A-304, amendment C-083): the embedded files,
// the applications live by SSE (event "apps", the same as GET /api/apps),
// the result of every Set as event "set", and the guards and limits common
// to every demo.
func TestWebUI(t *testing.T) {
	t.Parallel()
	b := demotest.StartBroker(t)
	startLogger(t, b)
	s := startService(t, b, `{"apps": ["Utility/`+loggerIL+`"], "requestTimeoutSeconds": 5}`, demotest.UIKeepAliveEnv+"=200")
	ui := demotest.WaitUI(t, s.addr)
	ui.CheckStatic(t, "OEC Config")

	ev := ui.Events(t)
	first, _ := ev.Event(t, 0, "apps", nil)
	if _, ok := first["connected"].(bool); !ok {
		t.Errorf("first event %v", first)
	}
	ev.Event(t, 0, "apps", func(d map[string]any) bool {
		e := config(appsEntry(d, "Utility", loggerIL), loggerIL, loggerContext)
		return d["connected"] == true && e != nil && e["writable"] == true && value(e, "minEventLevel") == "Informational"
	})

	// A Set from the UI: its result in the answer, as event "set" and the
	// changed Config as event "apps".
	from := ev.Len()
	body, err := json.Marshal(setBody("Utility", loggerIL, loggerContext, map[string]string{"minEventLevel": "Debug"}))
	if err != nil {
		t.Fatal(err)
	}
	resp, raw := ui.Do(t, "POST", "/api/set", "application/json", string(body))
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil || resp.StatusCode != 200 {
		t.Fatalf("POST /api/set: %d %s", resp.StatusCode, raw)
	}
	if n, sym, good := status(t, rec); n != 0 || sym != "Good" || !good {
		t.Errorf("Set: %d %s %v", n, sym, good)
	}
	set, _ := ev.Event(t, from, "set", func(d map[string]any) bool { return setRecord(d)["id"] == rec["id"] })
	if r := setRecord(set); r["app"] != loggerIL || r["context"] != loggerContext {
		t.Errorf("set event %v", set)
	} else if n, sym, _ := status(t, r); n != 0 || sym != "Good" {
		t.Errorf("set event status %d %s", n, sym)
	}
	ev.Event(t, from, "apps", func(d map[string]any) bool {
		return value(config(appsEntry(d, "Utility", loggerIL), loggerIL, loggerContext), "minEventLevel") == "Debug"
	})

	// A Set the logger refuses: its Status event as event "set" too.
	from = ev.Len()
	body, _ = json.Marshal(setBody("Utility", loggerIL, loggerContext, map[string]string{"minEventLevel": "Loud"}))
	if resp, raw := ui.Do(t, "POST", "/api/set", "application/json", string(body)); resp.StatusCode != 200 {
		t.Fatalf("POST /api/set: %d %s", resp.StatusCode, raw)
	}
	set, _ = ev.Event(t, from, "set", nil)
	if _, sym, good := status(t, setRecord(set)); good || sym == "Good" {
		t.Errorf("refused Set: %v", set)
	}
	// Checked before sending: no event.
	body, _ = json.Marshal(setBody("Utility", loggerIL, loggerContext, map[string]string{"nope": "1"}))
	if resp, _ := ui.Do(t, "POST", "/api/set", "application/json", string(body)); resp.StatusCode != 400 {
		t.Errorf("unknown object: %d", resp.StatusCode)
	}

	for _, path := range []string{"/api/apps?x=1", "/api/sets?x=1", "/api/events?x=1"} {
		if resp, raw := ui.Do(t, "GET", path, "", ""); resp.StatusCode != 400 {
			t.Errorf("%s: %d %s", path, resp.StatusCode, raw)
		}
	}
	if resp, _ := ui.Do(t, "POST", "/api/events", "application/json", "{}"); resp.StatusCode != 405 || resp.Header.Get("Allow") != "GET" {
		t.Errorf("POST /api/events: %d", resp.StatusCode)
	}

	ui.CheckGuards(t, "/api/apps")
	ui.CheckSlow(t)
	ui.CheckKeepAlive(t)
	ev.Close()
	ui.CheckLimits(t, "apps")
	for _, item := range ev.All() {
		if strings.HasPrefix(item.Type, "invalid") {
			t.Errorf("event stream: %s", item.Raw)
		}
	}
}

// OEC_UI_LISTEN: empty turns the Web UI off; an address that cannot be
// used stops the service with a message before it connects.
func TestWebUIListen(t *testing.T) {
	t.Parallel()
	b := demotest.StartBroker(t)
	root := b.Container(t, host)
	p := demotest.Start(t, exe(t), "OEC_ROOT="+root, "OEC_UI_LISTEN=")
	demotest.Eventually(t, "Web UI off", func() bool { return strings.Contains(p.Stderr(), "Web UI off") })
	// SIGTERM is handled once the application runs: wait for its Birth
	b.Expect(t, 0, "Birth", func(s demotest.Seen) bool {
		return s.Seen == "publish" && strings.Contains(s.Topic, "/Pub/MAM/")
	})
	if err := p.Stop(t); err != nil {
		t.Errorf("exit after SIGTERM: %v", err)
	}

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	for _, listen := range []string{"no-port", "127.0.0.1:99999", "127.0.0.1:", "[::1:80", ":8783", busy.Addr().String()} {
		p := demotest.Start(t, exe(t), "OEC_ROOT="+root, "OEC_UI_LISTEN="+listen)
		if err := p.Exit(t); err == nil || !strings.Contains(p.Stderr(), "Web UI") {
			t.Errorf("%s: exit %v, stderr %s", listen, err, p.Stderr())
		}
	}
	for _, keepAlive := range []string{"0", "15001", "x"} {
		p := demotest.Start(t, exe(t), "OEC_ROOT="+root, "OEC_UI_LISTEN=", demotest.UIKeepAliveEnv+"="+keepAlive)
		if err := p.Exit(t); err == nil || !strings.Contains(p.Stderr(), demotest.UIKeepAliveEnv) {
			t.Errorf("%s: exit %v, stderr %s", keepAlive, err, p.Stderr())
		}
	}
}
