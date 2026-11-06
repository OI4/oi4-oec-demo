package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	oec "github.com/OI4/oec/sdk/go"
)

// service runs the stations: one poll goroutine owns them and is the only
// one that calls the SDK after the start (assets, Data, events). The
// OnSet handlers run on the SDK thread and must not block: they check a
// proposal, hand it over and wake the poll goroutine (A-344).
type service struct {
	app  *oec.App
	host string
	out  io.Writer
	// metnoURL is the endpoint of met-no ("" = api.met.no); another one
	// only for tests (OEC_WEATHER_METNO_URL).
	metnoURL string

	mu    sync.Mutex
	next  *settings                    // accepted application Config, not yet applied
	moved map[*weatherStation]Location // accepted station Configs, not yet applied
	wake  chan struct{}

	// Owned by the poll goroutine (and by start before it runs).
	cur      settings
	provider Provider
	stations []*weatherStation
	// metno is kept across provider changes while the contact is the
	// same: its cache, its spacing and a throttling stay (ADR-0010).
	metno *MetNo
	// The Health of the application (A-349: NORMAL_0/100 or OFF_SPEC_3
	// with the share of good stations).
	health      oec.Health
	healthScore int
	// updated is the time of the last poll round.
	updated time.Time
	// ui is the Web UI (nil = off); the poll goroutine hands it a
	// snapshot after every round and change.
	ui *webUI
}

// weatherStation is a weather station: an asset represented on Level 3
// like the application (REQ-CMP-045), with its Config and Data.
type weatherStation struct {
	index  int
	loc    Location
	asset  *oec.Asset
	config *oec.Config
	data   *oec.DataSet
	// Whether the warnings are on (A-345).
	frost, storm bool
	// Whether the last observation failed (Health FAILURE_1/0).
	failed bool
	// For the Web UI: the last observations (at most uiHistory) and the
	// error of the last failed one.
	history []Observation
	lastErr string
}

// newService is the service; the application has the Health
// NORMAL_0/100.
func newService(app *oec.App, host, metnoURL string, out io.Writer) *service {
	return &service{app: app, host: host, out: out, metnoURL: metnoURL, moved: map[*weatherStation]Location{},
		wake: make(chan struct{}, 1), health: oec.Normal, healthScore: 100}
}

func (s *service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// start adds the application Config and the stations of the stored or
// default settings.
func (s *service) start() error {
	def := appConfig()
	cfg, err := s.app.This().AddConfig(def)
	if err != nil {
		return err
	}
	cur, err := parseSettings(read(cfg, def))
	if err != nil {
		fmt.Fprintln(os.Stderr, "oec-weather: stored Config:", err, "– default used")
	}
	if err := cfg.OnSet(func(proposed oec.ConfigValues) error {
		next, err := parseSettings(proposed)
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.next = &next
		s.mu.Unlock()
		s.signal()
		return nil
	}); err != nil {
		return err
	}
	if s.provider, err = s.newProvider(cur); err != nil {
		return err
	}
	s.cur = cur
	s.logf("config provider=%s stations=%d pollInterval=%s", cur.Provider, cur.Stations, cur.PollInterval)
	if err := s.resize(); err != nil {
		return err
	}
	s.publishUI()
	return nil
}

// newProvider is the provider of the settings.
func (s *service) newProvider(set settings) (Provider, error) {
	switch set.Provider {
	case "simulation":
		return Simulation{Seed: set.Seed}, nil
	case "met-no":
		if s.metno == nil || s.metno.contact != set.UserAgentContact {
			m, err := NewMetNo(set.UserAgentContact, s.metnoURL)
			if err != nil {
				return nil, err
			}
			m.Notice = s.notice
			s.metno = m
		}
		return s.metno, nil
	}
	return nil, fmt.Errorf("unknown provider %q", set.Provider)
}

// read are the current values of a Config ("" = unset).
func read(cfg *oec.Config, def oec.ConfigDef) oec.ConfigValues {
	v := oec.ConfigValues{}
	for _, g := range def.Groups {
		v[g.Name] = map[string]string{}
		for _, o := range g.Objects {
			x, _, _ := cfg.Get(g.Name, o.Name)
			v[g.Name][o.Name] = x
		}
	}
	return v
}

// stationProduct is the MAM of a station (A-341).
var stationProduct = oec.Product{
	ManufacturerURI:  "oi4.org",
	Model:            "OEC Weather Station",
	ProductCode:      "OEC-WEATHER-STATION",
	Manufacturer:     "Open Industry 4.0 Alliance",
	Description:      "Weather station of the OEC demo weather service",
	SoftwareRevision: version,
}

// addStation adds station i: asset with MAM and Health, its Config with
// the stored or default location, its Data Oi4Pv with Metadata.
func (s *service) addStation(i int) (*weatherStation, error) {
	asset, err := s.app.AddAsset(oec.AssetOptions{Product: stationProduct, SerialNumber: stationSerial(s.host, i)})
	if err != nil {
		return nil, err
	}
	st := &weatherStation{index: i, asset: asset}
	fail := func(err error) (*weatherStation, error) {
		_ = asset.Remove()
		return nil, err
	}
	if err := asset.SetHealth(oec.Normal, 100); err != nil {
		return fail(err)
	}
	def := stationConfig(i)
	if st.config, err = asset.AddConfig(def); err != nil {
		return fail(err)
	}
	if st.loc, err = parseLocation(i, read(st.config, def)); err != nil {
		fmt.Fprintf(os.Stderr, "oec-weather: stored Config of station %d: %v – default used\n", i, err)
	}
	if err := st.config.OnSet(func(proposed oec.ConfigValues) error {
		loc, err := parseLocation(i, proposed)
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.moved[st] = loc
		s.mu.Unlock()
		s.signal()
		return nil
	}); err != nil {
		return fail(err)
	}
	// Published on a change of at least the Precision of a field;
	// consumers may change Mode and Interval by Set PublicationList.
	if st.data, err = asset.AddData(oec.DataDef{Tag: oec.OI4PV, Fields: fields(), Writable: oec.WritableModeAndInterval}); err != nil {
		return fail(err)
	}
	s.logf("station %d %s at %g, %g, %g m added", i, st.loc.Name, st.loc.Latitude, st.loc.Longitude, st.loc.Altitude)
	return st, nil
}

// resize adds or removes stations to the configured number. A removed
// station leaves the PublicationList (REQ-RES-PUBLIST-017); MG defines no
// Close message for a represented asset (A-341).
func (s *service) resize() error {
	for len(s.stations) > s.cur.Stations {
		last := s.stations[len(s.stations)-1]
		if err := last.asset.Remove(); err != nil {
			return err
		}
		s.stations = s.stations[:len(s.stations)-1]
		s.logf("station %d %s removed", last.index, last.loc.Name)
	}
	for len(s.stations) < s.cur.Stations {
		st, err := s.addStation(len(s.stations) + 1)
		if err != nil {
			return err
		}
		s.stations = append(s.stations, st)
	}
	return nil
}

// apply applies the accepted Configs.
func (s *service) apply() error {
	s.mu.Lock()
	next, moved := s.next, s.moved
	s.next, s.moved = nil, map[*weatherStation]Location{}
	s.mu.Unlock()
	if next != nil {
		p, err := s.newProvider(*next)
		if err != nil {
			return err
		}
		s.cur, s.provider = *next, p
		s.logf("config provider=%s stations=%d pollInterval=%s", next.Provider, next.Stations, next.PollInterval)
		if err := s.resize(); err != nil {
			return err
		}
	}
	// In the order of the stations; a removed station is skipped.
	for _, st := range s.stations {
		if loc, ok := moved[st]; ok {
			st.loc = loc
			s.logf("station %d %s at %g, %g, %g m", st.index, loc.Name, loc.Latitude, loc.Longitude, loc.Altitude)
		}
	}
	return nil
}

// run is the poll goroutine: it observes every station each poll
// interval and right after an accepted Set Config. The first round waits
// for the connection, so the first Data and warnings are not dropped.
func (s *service) run(ctx context.Context) error {
	online := time.NewTicker(20 * time.Millisecond)
	for s.app.State() != oec.Online {
		select {
		case <-ctx.Done():
			online.Stop()
			return nil
		case <-online.C:
		}
	}
	online.Stop()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		case <-s.wake:
		}
		if err := s.apply(); err != nil {
			return err
		}
		s.publishUI()
		if err := s.poll(ctx); err != nil {
			return err
		}
		s.publishUI()
		timer.Reset(s.cur.PollInterval)
	}
}

// poll observes every station, publishes its Data, its Health and its
// warnings, then the Health of the application. A station without an
// observation publishes no Data (no invented values).
func (s *service) poll(ctx context.Context) error {
	now := time.Now().UTC()
	good := 0
	for _, st := range s.stations {
		o, err := s.provider.Observe(ctx, st.loc, now)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			fmt.Fprintf(os.Stderr, "oec-weather: station %d: %v\n", st.index, err)
			st.lastErr = err.Error()
			if err := s.stationHealth(ctx, st, err); err != nil {
				return err
			}
			continue
		}
		good++
		st.observed(o)
		if err := s.stationHealth(ctx, st, nil); err != nil {
			return err
		}
		if err := st.data.Update(dataValues(o)); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		// Events are dropped offline (ADR-0027): a warning is evaluated
		// online only, so a change is reported after a reconnect.
		if s.app.State() != oec.Online {
			continue
		}
		s.warn(st, "Frost", o.Temperature <= s.cur.FrostThreshold, &st.frost,
			map[string]float64{"Temperature": o.Temperature, "FrostThreshold": s.cur.FrostThreshold})
		s.warn(st, "Storm", o.WindSpeed >= s.cur.StormThreshold, &st.storm,
			map[string]float64{"WindSpeed": o.WindSpeed, "StormThreshold": s.cur.StormThreshold})
	}
	if err := s.appHealth(ctx, good); err != nil {
		return err
	}
	s.updated = now
	s.logf("poll %d stations", len(s.stations))
	return nil
}

// stationHealth sets the Health of a station on a change: FAILURE_1/0
// without an observation (err), else NORMAL_0/100; the change is a Syslog
// event of the application (Warning, Notice).
func (s *service) stationHealth(ctx context.Context, st *weatherStation, err error) error {
	failed := err != nil
	if failed == st.failed {
		return nil
	}
	st.failed = failed
	health, score := oec.Normal, 100
	if failed {
		health, score = oec.Failure, 0
	}
	if err := st.asset.SetHealth(health, score); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	if failed {
		s.logf("station %d %s failed: %v", st.index, st.loc.Name, err)
		s.syslog(oec.SyslogWarning, fmt.Sprintf("station %d %s: no weather data from %s: %v", st.index, st.loc.Name, s.provider.Name(), err))
	} else {
		s.logf("station %d %s recovered", st.index, st.loc.Name)
		s.syslog(oec.SyslogNotice, fmt.Sprintf("station %d %s: weather data from %s again", st.index, st.loc.Name, s.provider.Name()))
	}
	return nil
}

// appHealth sets the Health of the application on a change: NORMAL_0/100
// with every station good, else OFF_SPEC_3 (an uncertain signal, MG
// 8.3.2) with the share of good stations as HealthScore. Never
// FAILURE_1/0 about itself: that means not available (REQ-LIFE-012/017).
func (s *service) appHealth(ctx context.Context, good int) error {
	health, score := oec.Normal, 100
	if n := len(s.stations); good < n {
		health, score = oec.OffSpec, 100*good/n
	}
	if health == s.health && score == s.healthScore {
		return nil
	}
	if err := s.app.This().SetHealth(health, score); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	s.health, s.healthScore = health, score
	s.logf("health %s %d", health, score)
	return nil
}

// syslog publishes a Syslog event of the application while connected
// (the SDK drops events offline).
func (s *service) syslog(level oec.SyslogLevel, message string) {
	if s.app.State() != oec.Online {
		return
	}
	if err := s.app.Syslog(level, message); err != nil && !errors.Is(err, oec.ErrClosed) {
		fmt.Fprintf(os.Stderr, "oec-weather: syslog: %v\n", err)
	}
}

// notice reports a notice of a provider on standard error and as Syslog
// Warning.
func (s *service) notice(message string) {
	fmt.Fprintln(os.Stderr, "oec-weather:", message)
	s.syslog(oec.SyslogWarning, message)
}

// warn raises the Generic event of a warning when it begins (Number 1)
// and when it ends (Number 0) (A-345).
func (s *service) warn(st *weatherStation, level string, on bool, state *bool, details map[string]float64) {
	if on == *state {
		return
	}
	*state = on
	number, what := uint32(0), "ends"
	if on {
		number, what = 1, "begins"
	}
	err := st.asset.EmitEvent(oec.Event{Level: level, Number: number,
		Description: fmt.Sprintf("%s %s at %s", level, what, st.loc.Name), Details: details})
	if err != nil && !errors.Is(err, oec.ErrClosed) {
		fmt.Fprintf(os.Stderr, "oec-weather: event: %v\n", err)
	}
	s.logf("event station %d %s %s", st.index, level, what)
}

func (s *service) logf(format string, args ...any) {
	fmt.Fprintf(s.out, format+"\n", args...)
}
