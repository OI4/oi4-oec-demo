//go:build unix

package logger

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/OI4/oec/demos/internal/cembed"
	"github.com/OI4/oec/demos/internal/demotest"
)

// status is GET /api/status and the event "status" of the logger's Web UI.
type status struct {
	Version  string `json:"version"`
	State    string `json:"state"`
	Settings struct {
		Event         bool     `json:"event"`
		Health        bool     `json:"health"`
		Data          bool     `json:"data"`
		Sources       []string `json:"sources"`
		MinEventLevel string   `json:"minEventLevel"`
		Format        string   `json:"format"`
		File          string   `json:"file"`
		HistorySize   int      `json:"historySize"`
	} `json:"settings"`
	Records int `json:"records"`
	LastSeq int `json:"lastSeq"`
	Series  int `json:"series"`
}

// entry is a record of GET /api/records.
type entry struct {
	Seq    int    `json:"seq"`
	Record record `json:"record"`
}

type records struct {
	Records []entry `json:"records"`
	LastSeq int     `json:"lastSeq"`
}

func (r records) find(part string) *entry {
	for i, e := range r.Records {
		if strings.Contains(string(e.Record.Payload), part) {
			return &r.Records[i]
		}
	}
	return nil
}

func payloadHas(part string) func(map[string]any) bool {
	return func(d map[string]any) bool {
		rec, _ := d["record"].(map[string]any)
		return strings.Contains(fmt.Sprint(rec["payload"]), part)
	}
}

// The Web UI (ADR-0028 §3, A-303): the embedded files, the status with the
// settings of the OEC Config, the records live by SSE and as a filtered
// snapshot, the series, and the guards and limits common to every demo.
func TestWebUI(t *testing.T) {
	t.Parallel()
	r := start(t, demotest.UIKeepAliveEnv+"=200")
	ui := demotest.WaitUI(t, r.ui)
	ui.CheckStatic(t, "OEC Logger")

	ev := ui.Events(t)
	first, _ := ev.Event(t, 0, "status", nil)
	if s, _ := first["settings"].(map[string]any); s["event"] != true || s["minEventLevel"] != "Informational" {
		t.Errorf("first status %v", first)
	}
	var st status
	demotest.Eventually(t, "state online", func() bool {
		ui.JSON(t, "/api/status", &st)
		return st.State == "online"
	})
	if st.Version != "0.1.0" || !st.Settings.Health || st.Settings.Data || st.Settings.Format != "json" ||
		st.Settings.HistorySize != 1000 || st.Settings.Sources == nil {
		t.Errorf("status %+v", st)
	}

	from := ev.Len()
	r.inject(t, "OTConnector", pump, "Event", pump, "Syslog/Notice", syslog(13, "ui notice"))
	r.inject(t, "OTConnector", valve, "Event", valve, "Syslog/Warning", syslog(12, "ui warning"))
	r.inject(t, "OTConnector", pump, "Health", pump, "", map[string]any{"Health": "MAINTENANCE_REQUIRED_4", "HealthScore": 70})
	notice, i := ev.Event(t, from, "record", payloadHas("ui notice"))
	if rec := notice["record"].(map[string]any); rec["app"] != pump || rec["level"] != "Notice" || rec["resource"] != "Event" {
		t.Errorf("record event %v", notice)
	}
	warning, _ := ev.Event(t, i, "record", payloadHas("ui warning"))
	ev.Event(t, from, "record", payloadHas("MAINTENANCE_REQUIRED_4"))
	noticeSeq := int(notice["seq"].(float64))
	if seq := int(warning["seq"].(float64)); seq <= noticeSeq {
		t.Errorf("seq %d after %d", seq, noticeSeq)
	}

	// The snapshot and its filters.
	var all records
	ui.JSON(t, "/api/records", &all)
	if all.find("ui notice") == nil || all.find("ui warning") == nil || all.find("MAINTENANCE_REQUIRED_4") == nil {
		t.Fatalf("records %+v", all)
	}
	if e := all.find("ui notice"); e.Seq != noticeSeq || all.LastSeq < e.Seq {
		t.Errorf("records: seq %d, last %d", e.Seq, all.LastSeq)
	}
	for i := 1; i < len(all.Records); i++ {
		if all.Records[i].Seq <= all.Records[i-1].Seq {
			t.Errorf("records not ordered: %d after %d", all.Records[i].Seq, all.Records[i-1].Seq)
		}
	}
	for query, want := range map[string][]string{
		"resource=Event":                {"ui notice", "ui warning"},
		"resource=Health&q=pump":        {"MAINTENANCE_REQUIRED_4"},
		"level=Warning":                 {"ui warning"},
		"q=" + "VALVE%2FV-1":            {"ui warning"},
		"q=ui+not":                      {"ui notice"},
		"limit=1&resource=Health":       {"MAINTENANCE_REQUIRED_4"},
		fmt.Sprint("after=", noticeSeq): {"ui warning", "MAINTENANCE_REQUIRED_4"},
	} {
		var got records
		ui.JSON(t, "/api/records?"+query, &got)
		for _, part := range want {
			if got.find(part) == nil {
				t.Errorf("%s: no %q in %+v", query, part, got)
			}
		}
		for _, e := range got.Records {
			switch {
			case strings.HasPrefix(query, "resource=Event") && e.Record.Resource != "Event",
				strings.HasPrefix(query, "level=") && e.Record.Level != "Warning",
				strings.HasPrefix(query, "q=") && len(got.Records) != 1,
				strings.HasPrefix(query, "limit=1") && len(got.Records) != 1,
				strings.HasPrefix(query, "after=") && e.Seq <= noticeSeq:
				t.Errorf("%s: %+v", query, e)
			}
		}
	}
	for _, query := range []string{"limit=0", "limit=x", "limit=10001", "level=Loud", "resource=Metadata", "after=-1", "q=%zz", "x=1"} {
		if resp, raw := ui.Do(t, "GET", "/api/records?"+query, "", ""); resp.StatusCode != 400 {
			t.Errorf("%s: %d %s", query, resp.StatusCode, raw)
		}
	}

	var series struct {
		Series []struct {
			Resource string      `json:"resource"`
			App      string      `json:"app"`
			Source   *string     `json:"source"`
			Filter   *string     `json:"filter"`
			Field    string      `json:"field"`
			Points   [][]float64 `json:"points"`
		} `json:"series"`
	}
	ui.JSON(t, "/api/series", &series)
	found := false
	for _, s := range series.Series {
		if s.Resource == "Health" && s.App == pump && s.Field == "HealthScore" && s.Source != nil && *s.Source == pump && s.Filter == nil {
			found = len(s.Points) == 1 && len(s.Points[0]) == 2 && s.Points[0][1] == 70 && s.Points[0][0] > 1.7e12
		}
	}
	if !found {
		t.Errorf("no HealthScore series of the pump: %+v", series)
	}

	// The settings stay with the OEC Config: a Set shows on the status.
	from = ev.Len()
	if cfg, status := r.set(t, map[string]string{"minEventLevel": "Warning", "sources": "vendor.com/Pump vendor.com/Valve"}); cfg == nil {
		t.Fatalf("Set Config refused: %v", status)
	}
	ev.Event(t, from, "status", func(d map[string]any) bool {
		s, _ := d["settings"].(map[string]any)
		return s["minEventLevel"] == "Warning" && fmt.Sprint(s["sources"]) == "[vendor.com/Pump vendor.com/Valve]"
	})

	ui.CheckGuards(t, "/api/status")
	ui.CheckSlow(t)
	ui.CheckKeepAlive(t)
	ev.Close()
	ui.CheckLimits(t, "status")
}

// OEC_UI_LISTEN: empty turns the Web UI off; an address that cannot be
// used stops the logger with a message.
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
	for _, listen := range []string{"no-port", "127.0.0.1:99999", "127.0.0.1:", "[::1:80", busy.Addr().String()} {
		p := demotest.Start(t, exe(t), "OEC_ROOT="+r.root, "OEC_UI_LISTEN="+listen)
		if err := p.Exit(t); err == nil || !strings.Contains(p.Stderr(), "Web UI") {
			t.Errorf("%s: exit %v, stderr %s", listen, err, p.Stderr())
		}
	}
}

// The committed src/ui_assets.c holds the files of ui/ (cembed).
func TestUIAssets(t *testing.T) {
	want, err := cembed.Generate("ui")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("src/ui_assets.c")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("src/ui_assets.c is not current; from oec/demos run: %s", cembed.Command)
	}
}
