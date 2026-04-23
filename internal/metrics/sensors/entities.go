package sensors

import "time"

type TemperatureStat struct {
	SensorKey   string  `json:"sensor_key"`
	Temperature float64 `json:"temperature"`
	High        float64 `json:"high"`
	Critical    float64 `json:"critical"`
}

type TemperatureMetric struct {
	Timestamp time.Time         `json:"timestamp"`
	Sensors   []TemperatureStat `json:"sensors"`
}

func (t TemperatureMetric) GetTimestamp() time.Time { return t.Timestamp }
func (t TemperatureMetric) GetType() string         { return "sensors" }
