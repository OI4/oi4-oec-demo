package main

import (
	"context"
	"time"

	oec "github.com/OI4/oec/sdk/go"
)

// Location of a weather station: its name and coordinates (WGS 84) and
// its altitude above sea level in metres.
type Location struct {
	Name      string
	Latitude  float64
	Longitude float64
	Altitude  float64
}

// Observation is the weather at a station at a time.
type Observation struct {
	Time          time.Time
	Temperature   float64 // air temperature, °C
	Humidity      float64 // relative humidity, %
	Pressure      float64 // air pressure at sea level, hPa
	WindSpeed     float64 // m/s
	WindDirection float64 // direction the wind comes from, degrees clockwise from north
}

// Provider is a source of weather data: the offline simulation (C-080)
// or a weather service on the internet (MET Norway, C-081). Observe is
// called from the poll goroutine only; it ends when ctx ends.
type Provider interface {
	Name() string
	Observe(ctx context.Context, loc Location, at time.Time) (Observation, error)
}

// quantity is one field of the Data DataSet Oi4Pv of a station: its
// Metadata (REQ-RES-META-003), the resolution of its values and its value
// in an Observation.
type quantity struct {
	oec.Field
	resolution float64
	value      func(Observation) float64
}

// quantities are the fields of Oi4Pv: Pv is the temperature, the
// secondary values Sv1 … Sv4 follow (REQ-RES-DATA-002 … 005). The Ranges
// bound every value a provider delivers; Precision is the change that
// triggers a publication (REQ-RES-PUBLIST-013).
var quantities = []quantity{
	{oec.Field{Name: "Pv", Type: oec.TypeDouble, Unit: "°C", Description: "Air temperature",
		Range: &oec.Range{Low: -90, High: 60}, Precision: 0.1}, 0.1, func(o Observation) float64 { return o.Temperature }},
	{oec.Field{Name: "Sv1", Type: oec.TypeDouble, Unit: "%", Description: "Relative humidity",
		Range: &oec.Range{Low: 0, High: 100}, Precision: 1}, 0.1, func(o Observation) float64 { return o.Humidity }},
	{oec.Field{Name: "Sv2", Type: oec.TypeDouble, Unit: "hPa", Description: "Air pressure at sea level",
		Range: &oec.Range{Low: 870, High: 1085}, Precision: 0.1}, 0.1, func(o Observation) float64 { return o.Pressure }},
	{oec.Field{Name: "Sv3", Type: oec.TypeDouble, Unit: "m/s", Description: "Wind speed",
		Range: &oec.Range{Low: 0, High: 75}, Precision: 0.1}, 0.1, func(o Observation) float64 { return o.WindSpeed }},
	{oec.Field{Name: "Sv4", Type: oec.TypeDouble, Unit: "°", Description: "Wind direction (from, clockwise from north)",
		Range: &oec.Range{Low: 0, High: 360}, Precision: 1}, 1, func(o Observation) float64 { return o.WindDirection }},
}

// fields are the Fields of the DataSet Oi4Pv.
func fields() []oec.Field {
	out := make([]oec.Field, len(quantities))
	for i, q := range quantities {
		out[i] = q.Field
	}
	return out
}

// dataValues are the values of an Observation for DataSet.Update.
func dataValues(o Observation) map[string]float64 {
	out := make(map[string]float64, len(quantities))
	for _, q := range quantities {
		out[q.Name] = q.value(o)
	}
	return out
}
