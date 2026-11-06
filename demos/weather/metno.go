package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"
)

// The provider met-no: MET Norway Locationforecast 2.0 (ADR-0010, C-081)
// under the terms of service of api.met.no – an identifying User-Agent,
// no request before Expires, If-Modified-Since with the Last-Modified of
// the cached document, at most 4 decimals of the coordinates, no bursts,
// less traffic on 429. The data is licensed under CC BY 4.0 (and NLOD
// 2.0): "Based on data from MET Norway", changed by mapping and rounding
// to Oi4Pv (A-347, A-351).
const (
	metnoURL       = "https://api.met.no/weatherapi/locationforecast/2.0/compact"
	defaultSpacing = time.Second      // between two requests
	defaultTimeout = 30 * time.Second // of a request
	minThrottle    = 10 * time.Minute // pause after 429 without a longer Retry-After
	maxCached      = 16               // documents: the stations and their former locations
	maxBody        = 4 << 20          // octets of a response (a compact document has ~50 KB)
	maxDistance    = time.Hour        // between the time and the nearest forecast entry
)

// MetNo observes the weather from the Locationforecast of MET Norway. It
// is used by the poll goroutine only.
type MetNo struct {
	// BaseURL is the endpoint of the compact Locationforecast; another one
	// only for tests (OEC_WEATHER_METNO_URL).
	BaseURL string
	Client  *http.Client
	// Spacing is the minimum time between two requests (no bursts).
	Spacing time.Duration
	// Notice reports a notice of MET Norway once (203: deprecated).
	Notice func(string)

	contact   string
	cache     map[string]*metnoDoc
	last      time.Time // of the last request
	throttled time.Time // no request before (429)
	noticed   bool
}

// metnoDoc is a cached document: its timeseries and cache headers.
type metnoDoc struct {
	series   []metnoEntry
	expires  time.Time
	modified string
	used     time.Time
}

type metnoEntry struct {
	Time time.Time `json:"time"`
	Data struct {
		Instant struct {
			Details map[string]*float64 `json:"details"`
		} `json:"instant"`
	} `json:"data"`
}

type metnoJSON struct {
	Properties struct {
		Meta struct {
			Units map[string]string `json:"units"`
		} `json:"meta"`
		Timeseries []metnoEntry `json:"timeseries"`
	} `json:"properties"`
}

// metnoFields map the instant parameters to the fields of Oi4Pv (A-343)
// with the unit MET Norway uses.
var metnoFields = []struct{ param, unit, field string }{
	{"air_temperature", "celsius", "Pv"},
	{"relative_humidity", "%", "Sv1"},
	{"air_pressure_at_sea_level", "hPa", "Sv2"},
	{"wind_speed", "m/s", "Sv3"},
	{"wind_from_direction", "degrees", "Sv4"},
}

// NewMetNo is the provider with the contact of the operator for the
// User-Agent ("" is refused: MET Norway requires one); baseURL "" is the
// endpoint of api.met.no.
func NewMetNo(contact, baseURL string) (*MetNo, error) {
	if contact == "" {
		return nil, errors.New("provider met-no needs a userAgentContact: MET Norway requires an e-mail address or URL of the operator in the User-Agent")
	}
	if utf8.RuneCountInString(contact) > maxContact || !patterns[patternContact].MatchString(contact) {
		return nil, fmt.Errorf("provider met-no: userAgentContact %q is no e-mail address or URL (printable ASCII without spaces, at most %d)", contact, maxContact)
	}
	if baseURL == "" {
		baseURL = metnoURL
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	return &MetNo{BaseURL: baseURL, Client: &http.Client{Transport: transport, Timeout: defaultTimeout},
		Spacing: defaultSpacing, contact: contact, cache: map[string]*metnoDoc{}}, nil
}

func (m *MetNo) Name() string { return "met-no" }

// UserAgent identifies the service and its operator (MET Norway terms).
func (m *MetNo) UserAgent() string { return "oec-weather/" + version + " " + m.contact }

// metnoQuery is the query of a location: at most 4 decimals of the
// coordinates, the altitude in whole metres.
func metnoQuery(loc Location) string {
	return "lat=" + coordinate(loc.Latitude) + "&lon=" + coordinate(loc.Longitude) +
		"&altitude=" + strconv.Itoa(int(math.Round(loc.Altitude)))
}

func coordinate(x float64) string {
	r := math.Round(x*1e4) / 1e4
	if r == 0 {
		r = 0 // no "-0"
	}
	return strconv.FormatFloat(r, 'f', -1, 64)
}

// Observe is the forecast entry nearest to at of the location: from the
// cache before its Expires, else requested (conditionally). An error
// gives no values – an expired document is not used.
func (m *MetNo) Observe(ctx context.Context, loc Location, at time.Time) (Observation, error) {
	u := m.BaseURL + "?" + metnoQuery(loc)
	d := m.cache[u]
	if d == nil || !at.Before(d.expires) {
		if at.Before(m.throttled) {
			return Observation{}, fmt.Errorf("met-no: throttled (429) until %s", m.throttled.Format(time.RFC3339))
		}
		var err error
		if d, err = m.fetch(ctx, u, d, at); err != nil {
			return Observation{}, err
		}
	}
	d.used = at
	return d.observation(at)
}

// fetch requests the document of u; old is the expired cached one.
func (m *MetNo) fetch(ctx context.Context, u string, old *metnoDoc, at time.Time) (*metnoDoc, error) {
	if err := m.wait(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("met-no: %w", err)
	}
	// Accept-Encoding stays unset: the transport asks for gzip and
	// decompresses.
	req.Header.Set("User-Agent", m.UserAgent())
	if old != nil && old.modified != "" {
		req.Header.Set("If-Modified-Since", old.modified)
	}
	res, err := m.Client.Do(req)
	m.last = time.Now()
	if err != nil {
		return nil, fmt.Errorf("met-no: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
		_ = res.Body.Close()
	}()
	switch res.StatusCode {
	case http.StatusOK, http.StatusNonAuthoritativeInfo:
		body, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
		if err != nil {
			return nil, fmt.Errorf("met-no: %w", err)
		}
		if len(body) > maxBody {
			return nil, fmt.Errorf("met-no: response too large (more than %d octets)", maxBody)
		}
		d, err := parseForecast(body)
		if err != nil {
			return nil, err
		}
		if res.StatusCode == http.StatusNonAuthoritativeInfo && !m.noticed && m.Notice != nil {
			m.noticed = true
			m.Notice("met-no: 203 – the product Locationforecast 2.0 is deprecated, see https://api.met.no/weatherapi/locationforecast/2.0/status")
		}
		d.expires, d.modified = expires(res.Header), res.Header.Get("Last-Modified")
		m.store(u, d, at)
		return d, nil
	case http.StatusNotModified:
		if old == nil {
			return nil, errors.New("met-no: 304 Not Modified without a cached document")
		}
		old.expires = expires(res.Header)
		if lm := res.Header.Get("Last-Modified"); lm != "" {
			old.modified = lm
		}
		m.store(u, old, at)
		return old, nil
	case http.StatusTooManyRequests:
		pause := minThrottle
		if s, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && time.Duration(s)*time.Second > pause {
			pause = time.Duration(s) * time.Second
		} else if t, err := http.ParseTime(res.Header.Get("Retry-After")); err == nil && t.Sub(at) > pause {
			pause = t.Sub(at)
		}
		m.throttled = at.Add(pause)
		return nil, fmt.Errorf("met-no: %s – throttled until %s", res.Status, m.throttled.Format(time.RFC3339))
	case http.StatusForbidden:
		return nil, fmt.Errorf("met-no: %s – the User-Agent %q or the request is not accepted (MET Norway terms)", res.Status, m.UserAgent())
	}
	return nil, fmt.Errorf("met-no: %s", res.Status)
}

// wait keeps Spacing after the last request.
func (m *MetNo) wait(ctx context.Context) error {
	d := m.Spacing - time.Since(m.last)
	if m.last.IsZero() || d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// store caches d as the document of u and drops the least recently used
// beyond maxCached.
func (m *MetNo) store(u string, d *metnoDoc, at time.Time) {
	d.used = at
	m.cache[u] = d
	for len(m.cache) > maxCached {
		var oldest string
		for k, c := range m.cache {
			if k != u && (oldest == "" || c.used.Before(m.cache[oldest].used)) {
				oldest = k
			}
		}
		delete(m.cache, oldest)
	}
}

// expires is the time of the Expires header; missing or invalid = expired.
func expires(h http.Header) time.Time {
	t, err := http.ParseTime(h.Get("Expires"))
	if err != nil {
		return time.Time{}
	}
	return t
}

// parseForecast reads a compact document and checks its units.
func parseForecast(body []byte) (*metnoDoc, error) {
	var doc metnoJSON
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("met-no: no JSON forecast: %w", err)
	}
	for _, f := range metnoFields {
		if u := doc.Properties.Meta.Units[f.param]; u != f.unit {
			return nil, fmt.Errorf("met-no: unit of %s is %q, want %q", f.param, u, f.unit)
		}
	}
	return &metnoDoc{series: doc.Properties.Timeseries}, nil
}

// observation is the entry nearest to at, mapped to Oi4Pv: every value
// present and within the Range of its field, rounded to its resolution.
func (d *metnoDoc) observation(at time.Time) (Observation, error) {
	var best *metnoEntry
	for i := range d.series {
		e := &d.series[i]
		if best == nil || absDuration(e.Time.Sub(at)) < absDuration(best.Time.Sub(at)) {
			best = e
		}
	}
	if best == nil || absDuration(best.Time.Sub(at)) > maxDistance {
		return Observation{}, fmt.Errorf("met-no: no forecast within %s of %s", maxDistance, at.Format(time.RFC3339))
	}
	o := Observation{Time: best.Time.UTC()}
	for _, f := range metnoFields {
		v := best.Data.Instant.Details[f.param]
		if v == nil {
			return Observation{}, fmt.Errorf("met-no: %s missing at %s", f.param, best.Time.Format(time.RFC3339))
		}
		q := quantityOf(f.field)
		if *v < q.Range.Low || *v > q.Range.High || math.IsNaN(*v) {
			return Observation{}, fmt.Errorf("met-no: %s %v outside the Range %v … %v", f.param, *v, q.Range.Low, q.Range.High)
		}
		x := round(*v, q.resolution)
		if f.field == "Sv4" {
			x = math.Mod(x, 360)
		}
		*field(&o, f.field) = x
	}
	return o, nil
}

func quantityOf(name string) quantity {
	for _, q := range quantities {
		if q.Name == name {
			return q
		}
	}
	panic("no quantity " + name)
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
