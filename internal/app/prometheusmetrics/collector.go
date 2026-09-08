// Package prometheusmetrics provides Prometheus metrics collection for system stats.
package prometheusmetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	system "system-stats/internal/platform/system"
)

var (
	descCPUUsage     = prometheus.NewDesc("system_cpu_usage_percent", "Current CPU utilization percentage.", nil, nil)
	descCPULoadAvg1  = prometheus.NewDesc("system_cpu_load_avg_1", "System load average over 1 minute.", nil, nil)
	descCPULoadAvg5  = prometheus.NewDesc("system_cpu_load_avg_5", "System load average over 5 minutes.", nil, nil)
	descCPULoadAvg15 = prometheus.NewDesc("system_cpu_load_avg_15", "System load average over 15 minutes.", nil, nil)

	descMemUsage = prometheus.NewDesc("system_memory_usage_percent", "Current memory utilization percentage.", nil, nil)
	descMemUsed  = prometheus.NewDesc("system_memory_used_bytes", "Memory currently in use, in bytes.", nil, nil)
	descMemTotal = prometheus.NewDesc("system_memory_total_bytes", "Total physical memory, in bytes.", nil, nil)

	descDiskUsage = prometheus.NewDesc("system_disk_usage_percent", "Current disk utilization percentage.", nil, nil)
	descDiskUsed  = prometheus.NewDesc("system_disk_used_bytes", "Disk space currently in use, in bytes.", nil, nil)
	descDiskTotal = prometheus.NewDesc("system_disk_total_bytes", "Total disk space, in bytes.", nil, nil)

	descNetBytesSent = prometheus.NewDesc("system_network_bytes_sent_total", "Total bytes sent per network interface.", []string{"interface"}, nil)
	descNetBytesRecv = prometheus.NewDesc("system_network_bytes_recv_total", "Total bytes received per network interface.", []string{"interface"}, nil)
)

// SystemCollector implements prometheus.Collector over the metrics tick's
// latest snapshot. A scrape never scans the OS itself: doing so re-sampled
// every collector on top of the tick and, for network, reset the rate
// baseline the SSE stream relies on.
type SystemCollector struct {
	sys system.Service
}

func newSystemCollector(sys system.Service) *SystemCollector {
	return &SystemCollector{sys: sys}
}

// Describe sends all descriptor pointers to the channel.
func (c *SystemCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- descCPUUsage
	ch <- descCPULoadAvg1
	ch <- descCPULoadAvg5
	ch <- descCPULoadAvg15
	ch <- descMemUsage
	ch <- descMemUsed
	ch <- descMemTotal
	ch <- descDiskUsage
	ch <- descDiskUsed
	ch <- descDiskTotal
	ch <- descNetBytesSent
	ch <- descNetBytesRecv
}

// Collect exposes the latest tick snapshot. Modules missing from the snapshot
// (collector failed that tick, or no tick yet) are simply not exported.
func (c *SystemCollector) Collect(ch chan<- prometheus.Metric) {
	snap, ok := c.sys.Latest()
	if !ok {
		return
	}
	if m := snap.CPU; m != nil {
		ch <- prometheus.MustNewConstMetric(descCPUUsage, prometheus.GaugeValue, m.UsagePercent)
		ch <- prometheus.MustNewConstMetric(descCPULoadAvg1, prometheus.GaugeValue, m.LoadAvg1)
		ch <- prometheus.MustNewConstMetric(descCPULoadAvg5, prometheus.GaugeValue, m.LoadAvg5)
		ch <- prometheus.MustNewConstMetric(descCPULoadAvg15, prometheus.GaugeValue, m.LoadAvg15)
	}
	if m := snap.Memory; m != nil {
		ch <- prometheus.MustNewConstMetric(descMemUsage, prometheus.GaugeValue, m.UsagePercent)
		ch <- prometheus.MustNewConstMetric(descMemUsed, prometheus.GaugeValue, float64(m.Used))
		ch <- prometheus.MustNewConstMetric(descMemTotal, prometheus.GaugeValue, float64(m.Total))
	}
	if m := snap.Disk; m != nil {
		ch <- prometheus.MustNewConstMetric(descDiskUsage, prometheus.GaugeValue, m.UsagePercent)
		ch <- prometheus.MustNewConstMetric(descDiskUsed, prometheus.GaugeValue, float64(m.Used))
		ch <- prometheus.MustNewConstMetric(descDiskTotal, prometheus.GaugeValue, float64(m.Total))
	}
	if m := snap.Network; m != nil {
		for _, iface := range m.Interfaces {
			ch <- prometheus.MustNewConstMetric(descNetBytesSent, prometheus.CounterValue, float64(iface.BytesSent), iface.Name)
			ch <- prometheus.MustNewConstMetric(descNetBytesRecv, prometheus.CounterValue, float64(iface.BytesRecv), iface.Name)
		}
	}
}
