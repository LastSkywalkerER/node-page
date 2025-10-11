package entities

import "time"

// TemperatureStat represents a single temperature sensor reading.
type TemperatureStat struct {
	SensorKey   string  `json:"sensor_key"`
	Temperature float64 `json:"temperature"`
	High        float64 `json:"high"`
	Critical    float64 `json:"critical"`
}

// TemperatureMetric wraps a collection of sensors with a timestamp.
type TemperatureMetric struct {
	Timestamp time.Time         `json:"timestamp"`
	Sensors   []TemperatureStat `json:"sensors"`
}

func (t TemperatureMetric) GetTimestamp() time.Time { return t.Timestamp }
func (t TemperatureMetric) GetType() string         { return "sensors" }
