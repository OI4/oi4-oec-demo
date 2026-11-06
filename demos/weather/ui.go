package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The Web UI of the weather service (ADR-0028 §3, amendment C-083): the
// embedded files of ui/, GET /api/state and the event stream GET
// /api/events (SSE). Read only: the settings stay with the OEC Config of
// the service. No authentication, no TLS (A-305): it listens on loopback
// by default and refuses other hosts and sites like every demo UI.
//
// The limits are the same as those of the logger (C) and of the config
// service (Rust).
const (
	uiDefaultListen  = "127.0.0.1:8782"
	uiMaxConnections = 16
	uiMaxStreams     = 8
	uiMaxHead        = 8192
	uiMaxBody        = 64 << 10
	uiIOTimeout      = 5 * time.Second
	uiKeepAlive      = 15 * time.Second
	uiTick           = 250 * time.Millisecond
	// Observations per station in the history of the UI.
	uiHistory = 60
)

//go:embed ui/index.html ui/app.css ui/app.js
var uiFiles embed.FS

var uiTypes = map[string]string{
	"/":        "text/html; charset=utf-8",
	"/app.css": "text/css; charset=utf-8",
	"/app.js":  "text/javascript; charset=utf-8",
}

// uiAttribution credits MET Norway while met-no is the provider (A-351).
type uiAttribution struct {
	Text       string `json:"text"`
	Source     string `json:"source"`
	SourceURL  string `json:"sourceUrl"`
	License    string `json:"license"`
	LicenseURL string `json:"licenseUrl"`
}

var metnoCredit = uiAttribution{
	Text:       metnoAttribution,
	Source:     "MET Norway Locationforecast 2.0",
	SourceURL:  "https://api.met.no/weatherapi/locationforecast/2.0/documentation",
	License:    "CC BY 4.0",
	LicenseURL: "https://creativecommons.org/licenses/by/4.0/",
}

// uiObservation is an Observation of a station.
type uiObservation struct {
	Time          string   `json:"time"`
	Temperature   *float64 `json:"temperature"`
	Humidity      *float64 `json:"humidity"`
	Pressure      *float64 `json:"pressure"`
	WindSpeed     *float64 `json:"windSpeed"`
	WindDirection *float64 `json:"windDirection"`
}

func finite(x float64) *float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return nil
	}
	return &x
}

func observation(o Observation) uiObservation {
	return uiObservation{Time: o.Time.UTC().Format(time.RFC3339), Temperature: finite(o.Temperature),
		Humidity: finite(o.Humidity), Pressure: finite(o.Pressure), WindSpeed: finite(o.WindSpeed),
		WindDirection: finite(o.WindDirection)}
}

type uiStation struct {
	Index        int             `json:"index"`
	Name         string          `json:"name"`
	Latitude     float64         `json:"latitude"`
	Longitude    float64         `json:"longitude"`
	Altitude     float64         `json:"altitude"`
	SerialNumber string          `json:"serialNumber"`
	Health       string          `json:"health"`
	HealthScore  int             `json:"healthScore"`
	Failed       bool            `json:"failed"`
	Error        string          `json:"error"`
	Frost        bool            `json:"frost"`
	Storm        bool            `json:"storm"`
	Observation  *uiObservation  `json:"observation"`
	History      []uiObservation `json:"history"`
}

// uiSnapshot is the state of the service as the poll goroutine left it.
type uiSnapshot struct {
	Version             string         `json:"version"`
	Provider            string         `json:"provider"`
	PollIntervalSeconds float64        `json:"pollIntervalSeconds"`
	FrostThreshold      float64        `json:"frostThreshold"`
	StormThreshold      float64        `json:"stormThreshold"`
	Health              string         `json:"health"`
	HealthScore         int            `json:"healthScore"`
	Attribution         *uiAttribution `json:"attribution"`
	Stations            []uiStation    `json:"stations"`
	Updated             string         `json:"updated"`
}

// uiState is GET /api/state and the event "state": the snapshot and the
// connection state of the service.
type uiState struct {
	Type  string `json:"type,omitempty"`
	State string `json:"state"`
	uiSnapshot
}

// webUI serves the snapshots; the poll goroutine updates them.
type webUI struct {
	ln        net.Listener
	srv       *http.Server
	state     func() string
	keepAlive time.Duration
	streams   atomic.Int32

	mu      sync.Mutex
	snap    uiSnapshot
	changed chan struct{} // closed and replaced on every update
}

// listenUI binds the Web UI to address (host:port or [IPv6]:port).
func listenUI(address string) (*webUI, error) {
	host, port, err := net.SplitHostPort(address)
	if err == nil && (host == "" || !validPort(port)) {
		err = errors.New("no host or no port 0 … 65535")
	}
	if err != nil {
		return nil, fmt.Errorf("invalid address %s (host:port, [IPv6]:port): %v", address, err)
	}
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	return &webUI{ln: &limitListener{Listener: ln, slots: make(chan struct{}, uiMaxConnections)},
		keepAlive: uiKeepAlive, changed: make(chan struct{})}, nil
}

func validPort(p string) bool {
	n, err := strconv.Atoi(p)
	return err == nil && p != "" && p[0] != '+' && p[0] != '-' && n >= 0 && n <= 65535
}

func (u *webUI) addr() string { return u.ln.Addr().String() }

// serve answers requests until close; state is the connection state.
func (u *webUI) serve(state func() string) {
	u.state = state
	mux := http.NewServeMux()
	mux.HandleFunc("/", u.static)
	mux.HandleFunc("/api/state", u.getState)
	mux.HandleFunc("/api/events", u.events)
	u.srv = &http.Server{
		Handler:           guard(mux),
		ReadHeaderTimeout: uiIOTimeout,
		ReadTimeout:       uiIOTimeout,
		MaxHeaderBytes:    uiMaxHead,
	}
	u.srv.SetKeepAlivesEnabled(false)
	go func() {
		if err := u.srv.Serve(u.ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "oec-weather: Web UI:", err)
		}
	}()
}

func (u *webUI) close() {
	if u.srv == nil {
		_ = u.ln.Close()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if u.srv.Shutdown(ctx) != nil {
		_ = u.srv.Close()
	}
}

// update replaces the snapshot and wakes the event streams.
func (u *webUI) update(s uiSnapshot) {
	u.mu.Lock()
	u.snap = s
	close(u.changed)
	u.changed = make(chan struct{})
	u.mu.Unlock()
}

func (u *webUI) current(typ string) (uiState, <-chan struct{}) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return uiState{Type: typ, State: u.state(), uiSnapshot: u.snap}, u.changed
}

// ---- HTTP ------------------------------------------------------------------

// commonHeaders are on every response of a demo UI.
func commonHeaders(h http.Header) {
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Connection", "close")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		status, b = http.StatusInternalServerError, []byte(`{"error":"internal error"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// hostOK: localhost or an IP literal, with an optional port (no DNS
// name: DNS rebinding).
func hostOK(host string) bool {
	h := host
	if strings.HasPrefix(h, "[") {
		end := strings.Index(h, "]")
		if end < 0 {
			return false
		}
		rest := h[end+1:]
		if rest != "" && (!strings.HasPrefix(rest, ":") || !validPort(rest[1:])) {
			return false
		}
		a, err := netip.ParseAddr(h[1:end])
		return err == nil && a.Is6()
	}
	if i := strings.LastIndex(h, ":"); i >= 0 {
		if !validPort(h[i+1:]) {
			return false
		}
		h = h[:i]
	}
	if strings.EqualFold(h, "localhost") {
		return true
	}
	a, err := netip.ParseAddr(h)
	return err == nil && a.Is4()
}

// guard applies the checks of every demo UI before the route: Host (403),
// Origin (403), Transfer-Encoding (501), a POST without JSON (415), a
// body above uiMaxBody (413).
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		commonHeaders(w.Header())
		switch {
		case r.Host == "":
			fail(w, http.StatusBadRequest, "no Host")
		case !hostOK(r.Host):
			fail(w, http.StatusForbidden, "Host not allowed")
		case r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+r.Host:
			fail(w, http.StatusForbidden, "Origin not allowed")
		case len(r.TransferEncoding) > 0:
			fail(w, http.StatusNotImplemented, "Transfer-Encoding not supported")
		case r.Method == http.MethodPost && !isJSON(r.Header.Get("Content-Type")):
			fail(w, http.StatusUnsupportedMediaType, "Content-Type application/json required")
		case r.ContentLength > uiMaxBody:
			fail(w, http.StatusRequestEntityTooLarge, "body too large")
		default:
			r.Body = http.MaxBytesReader(w, r.Body, uiMaxBody)
			next.ServeHTTP(w, r)
		}
	})
}

func isJSON(ct string) bool {
	media, _, _ := strings.Cut(ct, ";")
	return strings.EqualFold(strings.TrimSpace(media), "application/json")
}

// get refuses every method but GET (405) and a query (400).
func get(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return false
	}
	if r.URL.RawQuery != "" {
		fail(w, http.StatusBadRequest, "no parameters")
		return false
	}
	return true
}

func (u *webUI) static(w http.ResponseWriter, r *http.Request) {
	ct, ok := uiTypes[r.URL.Path]
	if !ok {
		fail(w, http.StatusNotFound, "not found")
		return
	}
	if !get(w, r) {
		return
	}
	name := "ui" + r.URL.Path
	if r.URL.Path == "/" {
		name = "ui/index.html"
	}
	b, err := uiFiles.ReadFile(name)
	if err != nil {
		fail(w, http.StatusInternalServerError, "missing file")
		return
	}
	w.Header().Set("Content-Type", ct)
	_, _ = w.Write(b)
}

func (u *webUI) getState(w http.ResponseWriter, r *http.Request) {
	if !get(w, r) {
		return
	}
	s, _ := u.current("")
	writeJSON(w, http.StatusOK, s)
}

// events is the event stream: the state at once and after every poll or
// change of the connection state, a keep-alive comment after keepAlive
// without an event.
func (u *webUI) events(w http.ResponseWriter, r *http.Request) {
	if !get(w, r) {
		return
	}
	if u.streams.Add(1) > uiMaxStreams {
		u.streams.Add(-1)
		fail(w, http.StatusServiceUnavailable, "too many event streams")
		return
	}
	defer u.streams.Add(-1)
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	send := func(b []byte) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(uiIOTimeout))
		if _, err := w.Write(b); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	sendState := func() (string, <-chan struct{}, bool) {
		s, changed := u.current("state")
		b, err := json.Marshal(s)
		if err != nil {
			return s.State, changed, false
		}
		return s.State, changed, send(append(append([]byte("data: "), b...), '\n', '\n'))
	}
	state, changed, ok := sendState()
	tick := time.NewTicker(uiTick)
	defer tick.Stop()
	last := time.Now()
	for ok {
		select {
		case <-r.Context().Done():
			return
		case <-changed:
			state, changed, ok = sendState()
			last = time.Now()
		case <-tick.C:
			switch {
			case u.state() != state:
				state, changed, ok = sendState()
				last = time.Now()
			case time.Since(last) >= u.keepAlive:
				ok = send([]byte(": keep-alive\n\n"))
				last = time.Now()
			}
		}
	}
}

// limitListener accepts at most cap(slots) connections at a time; it
// answers a further one with 503 at once.
type limitListener struct {
	net.Listener
	slots chan struct{}
}

func (l *limitListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.slots <- struct{}{}:
			return &slotConn{Conn: c, release: func() { <-l.slots }}, nil
		default:
			go refuse(c)
		}
	}
}

func refuse(c net.Conn) {
	defer c.Close()
	body := `{"error":"too many connections"}`
	_ = c.SetDeadline(time.Now().Add(uiIOTimeout))
	_, _ = io.WriteString(c, "HTTP/1.1 503 Service Unavailable\r\nContent-Type: application/json\r\n"+
		"Content-Length: "+strconv.Itoa(len(body))+"\r\nConnection: close\r\nCache-Control: no-store\r\n"+
		"X-Content-Type-Options: nosniff\r\nContent-Security-Policy: default-src 'self'; frame-ancestors 'none'\r\n"+
		"Referrer-Policy: no-referrer\r\n\r\n"+body)
}

type slotConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *slotConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

// ---- snapshots of the poll goroutine ---------------------------------------

// uiSnapshot of the service; called by the poll goroutine only (and by
// start before it runs).
func (s *service) snapshot() uiSnapshot {
	snap := uiSnapshot{Version: version, Provider: s.cur.Provider, PollIntervalSeconds: s.cur.PollInterval.Seconds(),
		FrostThreshold: s.cur.FrostThreshold, StormThreshold: s.cur.StormThreshold,
		Health: s.health.String(), HealthScore: s.healthScore, Stations: []uiStation{}}
	if s.cur.Provider == "met-no" {
		credit := metnoCredit
		snap.Attribution = &credit
	}
	if !s.updated.IsZero() {
		snap.Updated = s.updated.UTC().Format(time.RFC3339)
	}
	for _, st := range s.stations {
		u := uiStation{Index: st.index, Name: st.loc.Name, Latitude: st.loc.Latitude, Longitude: st.loc.Longitude,
			Altitude: st.loc.Altitude, SerialNumber: stationSerial(s.host, st.index), Health: "NORMAL_0", HealthScore: 100,
			Failed: st.failed, Error: st.lastErr, Frost: st.frost, Storm: st.storm, History: make([]uiObservation, 0, len(st.history))}
		if st.failed {
			u.Health, u.HealthScore = "FAILURE_1", 0
		}
		for _, o := range st.history {
			u.History = append(u.History, observation(o))
		}
		if n := len(u.History); n > 0 && !st.failed {
			last := u.History[n-1]
			u.Observation = &last
		}
		snap.Stations = append(snap.Stations, u)
	}
	return snap
}

// publishUI hands the snapshot to the Web UI (if on).
func (s *service) publishUI() {
	if s.ui != nil {
		s.ui.update(s.snapshot())
	}
}

// observed records an observation of a station for the UI.
func (st *weatherStation) observed(o Observation) {
	st.history = append(st.history, o)
	if len(st.history) > uiHistory {
		st.history = append(st.history[:0], st.history[len(st.history)-uiHistory:]...)
	}
	st.lastErr = ""
}
