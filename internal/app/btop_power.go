package app

import (
	"math"
	"strings"
	"time"
)

// These readings preserve provenance before legacy normalization. They are
// deliberately excluded from exported metrics: the existing JSON contract stays intact.
type PowerReading struct {
	Watts  float64
	Valid  bool
	Source string
}

type PowerReadings struct {
	CPU, GPU, SRAM, ANE, RAM, Sensor PowerReading
}

func powerReading(w float64, valid bool, source string) PowerReading {
	return PowerReading{w, valid && source != "" && !math.IsNaN(w) && !math.IsInf(w, 0) && w >= 0 && w <= maxPlausiblePowerWatts, source}
}

func (p PowerReadings) gpu() PowerReading {
	r := p.GPU
	if r.Valid && p.SRAM.Valid {
		r.Watts += p.SRAM.Watts
		r.Source += "+SRAM"
	}
	return powerReading(r.Watts, r.Valid, r.Source)
}

func (p PowerReadings) sum(ram bool) PowerReading {
	items := []struct {
		name    string
		reading PowerReading
	}{{"CPU", p.CPU}, {"GPU", p.GPU}, {"SRAM", p.SRAM}, {"ANE", p.ANE}}
	if ram {
		items = append(items, struct {
			name    string
			reading PowerReading
		}{"RAM", p.RAM})
	}
	var watts float64
	var sources []string
	for _, item := range items {
		if item.reading.Valid {
			watts += item.reading.Watts
			sources = append(sources, item.name+":"+item.reading.Source)
		}
	}
	return powerReading(watts, len(sources) > 0, strings.Join(sources, "+"))
}

const btopHistoryCapacity = 1024

type btopPoint struct {
	At    time.Time
	Value float64
}
type btopSeries struct {
	points      [btopHistoryCapacity]btopPoint
	head, count int
	Source      string
	Min, Max    float64
	ValidCount  int
	Started     time.Time
	Scale       float64
	ScaleAt     time.Time
	extrema     map[string]btopExtrema
	axes        map[int]btopAxis
}

type btopExtrema struct {
	Min, Max float64
	Count    int
	Started  time.Time
}
type btopAxis struct {
	Ceiling float64
	At      time.Time
}

func (s *btopSeries) add(at time.Time, value float64, valid bool, source string) {
	valid = valid && !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
	if valid && s.Source != source {
		bank := s.extrema
		if bank == nil {
			bank = make(map[string]btopExtrema)
		}
		if s.Source != "" {
			bank[s.Source] = btopExtrema{s.Min, s.Max, s.ValidCount, s.Started}
		}
		*s = btopSeries{Source: source, extrema: bank}
		if previous, ok := bank[source]; ok {
			s.Min, s.Max, s.ValidCount, s.Started = previous.Min, previous.Max, previous.Count, previous.Started
		}
	}
	if !valid {
		value = math.NaN()
	}
	if valid {
		if s.ValidCount == 0 {
			s.Min, s.Max, s.Started = value, value, at
		} else {
			s.Min = math.Min(s.Min, value)
			s.Max = math.Max(s.Max, value)
		}
		s.ValidCount++
		target := math.Max(.1, value*1.1)
		if s.ScaleAt.IsZero() {
			s.Scale = target
		} else {
			elapsed := math.Max(0, at.Sub(s.ScaleAt).Seconds())
			s.Scale = math.Max(target, s.Scale*math.Exp(-elapsed/15))
		}
		s.ScaleAt = at
	}
	s.points[s.head] = btopPoint{at, value}
	s.head = (s.head + 1) % btopHistoryCapacity
	s.count = min(s.count+1, btopHistoryCapacity)
}

func (s *btopSeries) tail(n int) []btopPoint {
	n = min(max(0, n), s.count)
	result := make([]btopPoint, n)
	for i := range result {
		result[i] = s.points[(s.head-n+i+btopHistoryCapacity)%btopHistoryCapacity]
	}
	return result
}

type btopTelemetry struct {
	series        map[string]*btopSeries
	power         PowerReadings
	mainSource    string
	missingSensor int
	validSensor   int
}

var btopData = btopTelemetry{}

func (d *btopTelemetry) history(key string) *btopSeries {
	if d.series == nil {
		d.series = make(map[string]*btopSeries)
	}
	if d.series[key] == nil {
		d.series[key] = &btopSeries{}
	}
	return d.series[key]
}

func recordBtopPower(p PowerReadings, at time.Time) {
	btopData.power = p
	for key, r := range map[string]PowerReading{"sensor": p.Sensor, "sum": p.sum(true), "soc": p.sum(false), "ram": p.RAM, "cpu_w": p.CPU, "gpu_w": p.gpu(), "ane_w": p.ANE} {
		btopData.history(key).add(at, r.Watts, r.Valid, r.Source)
	}
	if btopData.mainSource == "" {
		if p.Sensor.Valid {
			btopData.mainSource = "sensor"
		} else {
			btopData.mainSource = "sum"
		}
	}
	if p.Sensor.Valid {
		btopData.missingSensor = 0
		btopData.validSensor++
	} else {
		btopData.missingSensor++
		btopData.validSensor = 0
	}
	if btopData.mainSource == "sensor" && btopData.missingSensor >= 3 {
		btopData.mainSource = "sum"
	}
	if btopData.mainSource == "sum" && btopData.validSensor >= 3 {
		btopData.mainSource = "sensor"
	}
}

func btopPowerOptions() BtopPowerConfig {
	c := BtopPowerConfig{Source: "auto"}
	if currentConfig.Htop != nil && currentConfig.Htop.Power != nil {
		c = *currentConfig.Htop.Power
		if c.Source == "" {
			c.Source = "auto"
		}
	}
	return c
}

func btopMainPower() (string, PowerReading) {
	key := btopPowerOptions().Source
	if key == "auto" {
		key = btopData.mainSource
		if key == "" {
			key = "sum"
		}
	}
	p := btopData.power
	switch key {
	case "sensor":
		return key, p.Sensor
	case "soc":
		return key, p.sum(false)
	case "ram":
		return key, p.RAM
	default:
		return "sum", p.sum(true)
	}
}

func btopOptional(v *bool, fallback bool) bool {
	if v != nil {
		return *v
	}
	return fallback
}
