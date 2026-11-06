package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// The contact of the tests: a reserved domain (RFC 2606), no real address.
const testContact = "oec-weather@example.invalid"

// forecastEntry is one entry of the timeseries of a fake forecast.
type forecastEntry struct {
	time                                              time.Time
	temperature, humidity, pressure, speed, direction float64
}

// forecast is a compact Locationforecast 2.0 document (docs.api.met.no,
// ForecastJSON) with the units of MET Norway; units overrides them.
func forecast(entries []forecastEntry, units map[string]string) []byte {
	u := map[string]string{
		"air_temperature": "celsius", "relative_humidity": "%", "air_pressure_at_sea_level": "hPa",
		"wind_speed": "m/s", "wind_from_direction": "degrees", "cloud_area_fraction": "%",
	}
	for k, v := range units {
		if v == "" {
			delete(u, k)
		} else {
			u[k] = v
		}
	}
	var series []any
	for _, e := range entries {
		series = append(series, map[string]any{
			"time": e.time.UTC().Format(time.RFC3339),
			"data": map[string]any{
				"instant": map[string]any{"details": map[string]any{
					"air_temperature": e.temperature, "relative_humidity": e.humidity,
					"air_pressure_at_sea_level": e.pressure, "wind_speed": e.speed,
					"wind_from_direction": e.direction, "cloud_area_fraction": 12.5,
				}},
				"next_1_hours": map[string]any{"summary": map[string]any{"symbol_code": "cloudy"}},
			},
		})
	}
	b, _ := json.Marshal(map[string]any{
		"type":     "Feature",
		"geometry": map[string]any{"type": "Point", "coordinates": []float64{8.6821, 50.1109, 112}},
		"properties": map[string]any{
			"meta":       map[string]any{"updated_at": "2026-11-01T10:00:00Z", "units": u},
			"timeseries": series,
		},
	})
	return b
}

// metReq is a request the fake MET server saw.
type metReq struct {
	at     time.Time
	query  string
	header http.Header
}

// fakeMET is a Locationforecast server for the tests: it checks the
// User-Agent like MET Norway (403 without an identifying one) and answers
// with reply.
type fakeMET struct {
	*httptest.Server
	mu    sync.Mutex
	seen  []metReq
	reply func(w http.ResponseWriter, r *http.Request)
}

func newFakeMET(t *testing.T, reply func(w http.ResponseWriter, r *http.Request)) *fakeMET {
	t.Helper()
	f := &fakeMET{reply: reply}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.seen = append(f.seen, metReq{at: time.Now(), query: r.URL.RawQuery, header: r.Header.Clone()})
		reply := f.reply
		f.mu.Unlock()
		if r.Method != http.MethodGet || r.URL.Path != "/weatherapi/locationforecast/2.0/compact" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if !identifying(r.UserAgent()) {
			http.Error(w, "identification required", http.StatusForbidden)
			return
		}
		reply(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeMET) url() string { return f.URL + "/weatherapi/locationforecast/2.0/compact" }

func (f *fakeMET) requests() []metReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]metReq(nil), f.seen...)
}

func (f *fakeMET) set(reply func(w http.ResponseWriter, r *http.Request)) {
	f.mu.Lock()
	f.reply = reply
	f.mu.Unlock()
}

// identifying is the check of the fake server: a product token with a
// version and a contact (an e-mail address or a URL), no generic client.
func identifying(ua string) bool {
	product, contact, ok := strings.Cut(ua, " ")
	if !ok || !strings.Contains(product, "/") || contact == "" {
		return false
	}
	for _, generic := range []string{"Go-http-client", "curl", "python-requests", "okhttp"} {
		if strings.HasPrefix(product, generic) {
			return false
		}
	}
	return strings.Contains(contact, "@") || strings.Contains(contact, ".")
}

// ok answers 200 with the document and the cache headers.
func ok(doc []byte, expires, modified time.Time) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		if !expires.IsZero() {
			w.Header().Set("Expires", expires.UTC().Format(http.TimeFormat))
		}
		if !modified.IsZero() {
			w.Header().Set("Last-Modified", modified.UTC().Format(http.TimeFormat))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(doc)
	}
}

func status(code int) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) { http.Error(w, http.StatusText(code), code) }
}

// clock is the time of the tests: a whole second, as the HTTP dates.
var clock = time.Date(2026, 11, 1, 12, 20, 0, 0, time.UTC)

// series are two hourly entries around clock.
func series() []forecastEntry {
	return []forecastEntry{
		{clock.Truncate(time.Hour), 7.34, 81.26, 1012.04, 3.16, 237.6},
		{clock.Truncate(time.Hour).Add(time.Hour), 8.0, 79.0, 1011.0, 4.0, 359.7},
	}
}

func testMetNo(t *testing.T, url string) *MetNo {
	t.Helper()
	m, err := NewMetNo(testContact, url)
	if err != nil {
		t.Fatal(err)
	}
	m.Spacing = 0
	return m
}

// The User-Agent identifies the service and its operator (MET Norway
// terms): product/version and the contact; without a contact, or with one
// that would break the header, there is no provider.
func TestMetNoUserAgent(t *testing.T) {
	m := testMetNo(t, "")
	if want := "oec-weather/" + version + " " + testContact; m.UserAgent() != want {
		t.Errorf("User-Agent %q, want %q", m.UserAgent(), want)
	}
	if !identifying(m.UserAgent()) {
		t.Errorf("User-Agent %q not identifying", m.UserAgent())
	}
	if m.BaseURL != metnoURL || metnoURL != "https://api.met.no/weatherapi/locationforecast/2.0/compact" {
		t.Errorf("base URL %q", m.BaseURL)
	}
	if m.Name() != "met-no" {
		t.Errorf("name %q", m.Name())
	}
	for _, bad := range []string{"", "a b@example.org", "x\r\nX-Evil: 1", "café@example.org", strings.Repeat("a", maxContact+1)} {
		if _, err := NewMetNo(bad, ""); err == nil || !strings.Contains(err.Error(), "userAgentContact") {
			t.Errorf("contact %q: %v", bad, err)
		}
	}
}

// The query: at most 4 decimals of the coordinates (more are refused by
// MET Norway with 403), the altitude in whole metres.
func TestMetNoQuery(t *testing.T) {
	for _, c := range []struct {
		loc  Location
		want string
	}{
		{frankfurt, "lat=50.1109&lon=8.6821&altitude=112"},
		{Location{Latitude: 50.123456, Longitude: -8.000049, Altitude: 112.6}, "lat=50.1235&lon=-8&altitude=113"},
		{Location{Latitude: -0.00001, Longitude: 179.99999, Altitude: -0.4}, "lat=0&lon=180&altitude=0"},
		{Location{Latitude: 1.1, Longitude: 103.8198, Altitude: 15}, "lat=1.1&lon=103.8198&altitude=15"},
	} {
		if got := metnoQuery(c.loc); got != c.want {
			t.Errorf("%+v: %q, want %q", c.loc, got, c.want)
		}
	}
}

// A 200 answer: the entry of the current hour mapped to Oi4Pv (A-343),
// rounded to the resolution of the fields; the request carries the
// User-Agent and accepts gzip (by the transport, MET Norway terms).
func TestMetNoObserve(t *testing.T) {
	f := newFakeMET(t, ok(forecast(series(), nil), clock.Add(30*time.Minute), clock.Add(-10*time.Minute)))
	m := testMetNo(t, f.url())
	o, err := m.Observe(context.Background(), frankfurt, clock)
	if err != nil {
		t.Fatal(err)
	}
	want := Observation{Time: clock.Truncate(time.Hour), Temperature: 7.3, Humidity: 81.3, Pressure: 1012, WindSpeed: 3.2, WindDirection: 238}
	if o != want {
		t.Errorf("%+v, want %+v", o, want)
	}
	reqs := f.requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests", len(reqs))
	}
	h := reqs[0].header
	if h.Get("User-Agent") != m.UserAgent() || h.Get("Accept-Encoding") != "gzip" || h.Get("If-Modified-Since") != "" {
		t.Errorf("headers %v", h)
	}
	if reqs[0].query != "lat=50.1109&lon=8.6821&altitude=112" {
		t.Errorf("query %q", reqs[0].query)
	}
	// The next entry is nearer at 12:40; 359.7° is north (0°).
	o, err = m.Observe(context.Background(), frankfurt, clock.Add(20*time.Minute))
	if err != nil || o.WindDirection != 0 || o.Temperature != 8 {
		t.Errorf("12:40: %+v, %v", o, err)
	}
}

// Caching (MET Norway terms): no request before Expires; after it a
// conditional request with If-Modified-Since = Last-Modified; 304 keeps
// the data and renews Expires; 200 replaces it.
func TestMetNoCache(t *testing.T) {
	modified := clock.Add(-10 * time.Minute)
	f := newFakeMET(t, ok(forecast(series(), nil), clock.Add(30*time.Minute), modified))
	m := testMetNo(t, f.url())
	ctx := context.Background()
	first, err := m.Observe(ctx, frankfurt, clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Observe(ctx, frankfurt, clock.Add(29*time.Minute)); err != nil || len(f.requests()) != 1 {
		t.Fatalf("before Expires: %v, %d requests", err, len(f.requests()))
	}

	f.set(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-Modified-Since") != modified.Format(http.TimeFormat) {
			t.Errorf("If-Modified-Since %q", r.Header.Get("If-Modified-Since"))
		}
		w.Header().Set("Expires", clock.Add(time.Hour).Format(http.TimeFormat))
		w.WriteHeader(http.StatusNotModified)
	})
	o, err := m.Observe(ctx, frankfurt, clock.Add(30*time.Minute))
	if err != nil || len(f.requests()) != 2 || o.Temperature != 8 {
		t.Fatalf("304: %+v, %v, %d requests", o, err, len(f.requests()))
	}
	if _, err := m.Observe(ctx, frankfurt, clock.Add(59*time.Minute)); err != nil || len(f.requests()) != 2 {
		t.Fatalf("before the renewed Expires: %v, %d requests", err, len(f.requests()))
	}

	later := []forecastEntry{{clock.Add(time.Hour).Truncate(time.Hour), -1.04, 90, 1000, 10, 90}}
	f.set(ok(forecast(later, nil), clock.Add(2*time.Hour), clock.Add(time.Hour)))
	o, err = m.Observe(ctx, frankfurt, clock.Add(time.Hour))
	if err != nil || o.Temperature != -1 || o == first {
		t.Fatalf("new 200: %+v, %v", o, err)
	}
	// Another location is another document.
	if _, err := m.Observe(ctx, oslo, clock.Add(time.Hour)); err != nil || len(f.requests()) != 4 {
		t.Fatalf("Oslo: %v, %d requests", err, len(f.requests()))
	}
}

// Without Expires the document is expired at once: every observation
// asks again, conditionally.
func TestMetNoNoExpires(t *testing.T) {
	modified := clock.Add(-time.Minute)
	f := newFakeMET(t, ok(forecast(series(), nil), time.Time{}, modified))
	m := testMetNo(t, f.url())
	for i := 0; i < 3; i++ {
		if _, err := m.Observe(context.Background(), frankfurt, clock); err != nil {
			t.Fatal(err)
		}
	}
	reqs := f.requests()
	if len(reqs) != 3 || reqs[1].header.Get("If-Modified-Since") != modified.Format(http.TimeFormat) {
		t.Fatalf("%d requests %v", len(reqs), reqs)
	}
}

// Failures give an error and no values: HTTP errors (also after a
// cached document expired – no stale data), throttling, broken or
// unexpected documents, no entry for the time.
func TestMetNoErrors(t *testing.T) {
	good := forecast(series(), nil)
	exp := clock.Add(time.Minute)
	for name, c := range map[string]struct {
		reply func(http.ResponseWriter, *http.Request)
		want  string
	}{
		"400":            {status(400), "400"},
		"404":            {status(404), "404"},
		"500":            {status(500), "500"},
		"503":            {status(503), "503"},
		"broken JSON":    {ok(good[:len(good)/2], exp, time.Time{}), "JSON"},
		"not JSON":       {ok([]byte("<html>maintenance</html>"), exp, time.Time{}), "JSON"},
		"no timeseries":  {ok(forecast(nil, nil), exp, time.Time{}), "no forecast"},
		"too late":       {ok(forecast([]forecastEntry{{clock.Add(3 * time.Hour), 1, 1, 1000, 1, 1}}, nil), exp, time.Time{}), "no forecast"},
		"fahrenheit":     {ok(forecast(series(), map[string]string{"air_temperature": "fahrenheit"}), exp, time.Time{}), "unit"},
		"no unit":        {ok(forecast(series(), map[string]string{"wind_speed": ""}), exp, time.Time{}), "unit"},
		"out of range":   {ok(forecast([]forecastEntry{{clock, 7, 101, 1000, 1, 1}}, nil), exp, time.Time{}), "Range"},
		"missing value":  {ok([]byte(strings.Replace(string(good), `"air_temperature":7.34,`, "", 1)), exp, time.Time{}), "air_temperature"},
		"304 unasked":    {func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotModified) }, "304"},
		"wrong location": {ok(good, exp, time.Time{}), ""},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeMET(t, c.reply)
			m := testMetNo(t, f.url())
			if name == "wrong location" {
				m.BaseURL = f.URL + "/weatherapi/locationforecast/2.0/complete"
				c.want = "404"
			}
			o, err := m.Observe(context.Background(), frankfurt, clock)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("%+v, %v; want an error with %q", o, err, c.want)
			}
			if o != (Observation{}) {
				t.Errorf("values %+v with the error", o)
			}
		})
	}

	// A cached document that expired is not used when the renewal fails.
	f := newFakeMET(t, ok(good, clock.Add(time.Minute), clock))
	m := testMetNo(t, f.url())
	if _, err := m.Observe(context.Background(), frankfurt, clock); err != nil {
		t.Fatal(err)
	}
	f.set(status(502))
	if o, err := m.Observe(context.Background(), frankfurt, clock.Add(2*time.Minute)); err == nil {
		t.Fatalf("stale %+v", o)
	}
	f.set(ok(good, clock.Add(time.Hour), clock))
	if _, err := m.Observe(context.Background(), frankfurt, clock.Add(3*time.Minute)); err != nil {
		t.Fatalf("recovery: %v", err)
	}
}

// A wrong User-Agent is detected: MET Norway answers 403, the error says
// why.
func TestMetNoForbidden(t *testing.T) {
	f := newFakeMET(t, ok(forecast(series(), nil), clock.Add(time.Hour), clock))
	m := testMetNo(t, f.url())
	m.contact = "nobody"
	_, err := m.Observe(context.Background(), frankfurt, clock)
	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "User-Agent") {
		t.Fatalf("%v", err)
	}
}

// 429 throttles: no request until Retry-After (at least 10 minutes) has
// passed, for every location (MET Norway terms: reduce the traffic).
func TestMetNoThrottled(t *testing.T) {
	f := newFakeMET(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "1200")
		http.Error(w, "slow down", http.StatusTooManyRequests)
	})
	m := testMetNo(t, f.url())
	ctx := context.Background()
	if _, err := m.Observe(ctx, frankfurt, clock); err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("%v", err)
	}
	f.set(ok(forecast(series(), nil), clock.Add(time.Hour), clock))
	if _, err := m.Observe(ctx, oslo, clock.Add(19*time.Minute)); err == nil || !strings.Contains(err.Error(), "throttled") || len(f.requests()) != 1 {
		t.Fatalf("within Retry-After: %v, %d requests", err, len(f.requests()))
	}
	if _, err := m.Observe(ctx, frankfurt, clock.Add(20*time.Minute)); err != nil || len(f.requests()) != 2 {
		t.Fatalf("after Retry-After: %v, %d requests", err, len(f.requests()))
	}
	// Without Retry-After the pause is 10 minutes.
	f.set(status(http.StatusTooManyRequests))
	_, _ = m.Observe(ctx, oslo, clock.Add(20*time.Minute))
	if _, err := m.Observe(ctx, oslo, clock.Add(29*time.Minute)); err == nil || len(f.requests()) != 3 {
		t.Fatalf("within 10 minutes: %v, %d requests", err, len(f.requests()))
	}
}

// 203: the product is deprecated – the data is used, the notice is
// reported once.
func TestMetNoDeprecated(t *testing.T) {
	doc := forecast(series(), nil)
	f := newFakeMET(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNonAuthoritativeInfo)
		_, _ = w.Write(doc)
	})
	m := testMetNo(t, f.url())
	var notices []string
	m.Notice = func(s string) { notices = append(notices, s) }
	for i := 0; i < 2; i++ {
		if _, err := m.Observe(context.Background(), frankfurt, clock); err != nil {
			t.Fatal(err)
		}
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "deprecated") {
		t.Errorf("notices %q", notices)
	}
}

// A hanging server: the request ends at the timeout of the client or with
// the context.
func TestMetNoTimeout(t *testing.T) {
	release := make(chan struct{})
	f := newFakeMET(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)
	m := testMetNo(t, f.url())
	m.Client.Timeout = 200 * time.Millisecond
	start := time.Now()
	if _, err := m.Observe(context.Background(), frankfurt, clock); err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("timeout: %v after %v", err, time.Since(start))
	}
	m.Client.Timeout = time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := m.Observe(ctx, oslo, clock); err == nil || ctx.Err() == nil {
		t.Fatalf("context: %v", err)
	}
}

// No bursts: two requests are at least Spacing apart; a wait ends with
// the context.
func TestMetNoSpacing(t *testing.T) {
	f := newFakeMET(t, ok(forecast(series(), nil), clock.Add(time.Hour), clock))
	m := testMetNo(t, f.url())
	m.Spacing = 300 * time.Millisecond
	ctx := context.Background()
	for _, loc := range []Location{frankfurt, oslo, zugspitze} {
		if _, err := m.Observe(ctx, loc, clock); err != nil {
			t.Fatal(err)
		}
	}
	reqs := f.requests()
	for i := 1; i < len(reqs); i++ {
		if d := reqs[i].at.Sub(reqs[i-1].at); d < 290*time.Millisecond {
			t.Errorf("requests %d and %d %v apart", i-1, i, d)
		}
	}
	// Cached: no wait.
	start := time.Now()
	if _, err := m.Observe(ctx, oslo, clock); err != nil || time.Since(start) > 100*time.Millisecond {
		t.Errorf("cached: %v after %v", err, time.Since(start))
	}
	m.Spacing = time.Hour
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := m.Observe(short, Location{Latitude: 1, Longitude: 2}, clock); err == nil {
		t.Fatal("no wait")
	}
	if len(f.requests()) != 3 {
		t.Errorf("%d requests", len(f.requests()))
	}
	if d := defaultSpacing; d < time.Second {
		t.Errorf("default spacing %v", d)
	}
}

// The cache keeps at most maxCached documents (stations and their former
// locations).
func TestMetNoCacheBound(t *testing.T) {
	f := newFakeMET(t, ok(forecast(series(), nil), clock.Add(time.Hour), clock))
	m := testMetNo(t, f.url())
	for i := 0; i < 3*maxCached; i++ {
		loc := Location{Latitude: float64(i) / 10, Longitude: 8}
		if _, err := m.Observe(context.Background(), loc, clock.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if len(m.cache) > maxCached {
		t.Errorf("%d cached", len(m.cache))
	}
}

// The response body is limited.
func TestMetNoBodyLimit(t *testing.T) {
	f := newFakeMET(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"x":"`))
		_, _ = w.Write([]byte(strings.Repeat("a", maxBody+1)))
		_, _ = w.Write([]byte(`"}`))
	})
	m := testMetNo(t, f.url())
	if _, err := m.Observe(context.Background(), frankfurt, clock); err == nil || !strings.Contains(err.Error(), "large") {
		t.Fatalf("%v", err)
	}
}

// Live against api.met.no – opt-in only, never in the gate (G-4.7):
// OEC_WEATHER_LIVE_CONTACT is the contact of the User-Agent (an e-mail
// address or URL of the operator, MET Norway terms).
func TestMetNoLive(t *testing.T) {
	contact := os.Getenv("OEC_WEATHER_LIVE_CONTACT")
	if contact == "" {
		t.Skip("OEC_WEATHER_LIVE_CONTACT not set (opt-in, live against api.met.no)")
	}
	m, err := NewMetNo(contact, "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, loc := range []Location{frankfurt, oslo} {
		o, err := m.Observe(context.Background(), loc, now)
		if err != nil {
			t.Fatalf("%s: %v", loc.Name, err)
		}
		for _, q := range quantities {
			if v := q.value(o); v < q.Range.Low || v > q.Range.High || math.IsNaN(v) {
				t.Errorf("%s: %s = %v", loc.Name, q.Name, v)
			}
		}
		e := m.cache[m.BaseURL+"?"+metnoQuery(loc)]
		fmt.Printf("live %s: %+v (Expires %s, Last-Modified %s)\n", loc.Name, o, e.expires.Format(time.RFC3339), e.modified)
		// Cached until Expires: no second request.
		if _, err := m.Observe(context.Background(), loc, now); err != nil {
			t.Fatal(err)
		}
	}
}
