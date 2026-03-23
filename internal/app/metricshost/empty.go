package metricshost

// EmptyCPUPayload is the JSON shape for valid host with no stored metrics yet.
func EmptyCPUPayload() map[string]any {
	return map[string]any{"latest": nil, "history": []any{}}
}

// EmptyMemoryPayload returns an empty memory metrics response.
func EmptyMemoryPayload() map[string]any {
	return map[string]any{"latest": nil, "history": []any{}}
}

// EmptyDiskPayload returns an empty disk metrics response.
func EmptyDiskPayload() map[string]any {
	return map[string]any{"latest": nil, "history": []any{}}
}

// EmptyNetworkPayload returns an empty network metrics response.
func EmptyNetworkPayload() map[string]any {
	return map[string]any{"latest": nil, "history": []any{}}
}

// EmptyDockerPayload returns an empty Docker metrics response.
func EmptyDockerPayload() map[string]any {
	return map[string]any{"latest": nil, "history": []any{}, "docker_available": false}
}

// EmptySensorsPayload returns sensors for a host we cannot read locally (JSON null — frontend distinguishes from empty Linux readings).
func EmptySensorsPayload() map[string]any {
	return map[string]any{"sensors": nil}
}

// EmptyCurrentMetricsPayload matches /metrics/current shape when no live snapshot exists for the host.
func EmptyCurrentMetricsPayload() map[string]any {
	return map[string]any{
		"timestamp": nil,
		"cpu":       nil,
		"memory":    nil,
		"disk":      nil,
		"network":   nil,
		"docker":    nil,
	}
}
