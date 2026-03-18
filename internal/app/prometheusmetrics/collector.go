// Package prometheusmetrics provides Prometheus metrics collection for system stats.
package prometheusmetrics

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	cpuservice "system-stats/internal/modules/cpu/application"
	diskservice "system-stats/internal/modules/disk/application"
	memoryservice "system-stats/internal/modules/memory/application"
	networkservice "system-stats/internal/modules/network/application"
)

var (
	descCPUUsage    = prometheus.NewDesc("system_cpu_usage_percent", "Current CPU utilization percentage.", nil, nil)
	descCPULoadAvg1 = prometheus.NewDesc("system_cpu_load_avg_1", "System load average over 1 minute.", nil, nil)
	descCPULoadAvg5 = prometheus.NewDesc("system_cpu_load_avg_5", "System load average over 5 minutes.", nil, nil)
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

// SystemCollector implements prometheus.Collector and exposes live system metrics.
type SystemCollector struct {
	cpu     cpuservice.Service
	memory  memoryservice.Service
	disk    diskservice.Service
	network networkservice.Service
}

func newSystemCollector(cpu cpuservice.Service, mem memoryservice.Service, disk diskservice.Service, net networkservice.Service) *SystemCollector {
	return &SystemCollector{cpu: cpu, memory: mem, disk: disk, network: net}
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

// Collect fetches fresh metrics from each service and sends them to the channel.
func (c *SystemCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if m, err := c.cpu.Collect(ctx); err == nil {
		ch <- prometheus.MustNewConstMetric(descCPUUsage, prometheus.GaugeValue, m.UsagePercent)
		ch <- prometheus.MustNewConstMetric(descCPULoadAvg1, prometheus.GaugeValue, m.LoadAvg1)
		ch <- prometheus.MustNewConstMetric(descCPULoadAvg5, prometheus.GaugeValue, m.LoadAvg5)
		ch <- prometheus.MustNewConstMetric(descCPULoadAvg15, prometheus.GaugeValue, m.LoadAvg15)
	}

	if m, err := c.memory.Collect(ctx); err == nil {
		ch <- prometheus.MustNewConstMetric(descMemUsage, prometheus.GaugeValue, m.UsagePercent)
		ch <- prometheus.MustNewConstMetric(descMemUsed, prometheus.GaugeValue, float64(m.Used))
		ch <- prometheus.MustNewConstMetric(descMemTotal, prometheus.GaugeValue, float64(m.Total))
	}

	if m, err := c.disk.Collect(ctx); err == nil {
		ch <- prometheus.MustNewConstMetric(descDiskUsage, prometheus.GaugeValue, m.UsagePercent)
		ch <- prometheus.MustNewConstMetric(descDiskUsed, prometheus.GaugeValue, float64(m.Used))
		ch <- prometheus.MustNewConstMetric(descDiskTotal, prometheus.GaugeValue, float64(m.Total))
	}

	if m, err := c.network.Collect(ctx); err == nil {
		for _, iface := range m.Interfaces {
			ch <- prometheus.MustNewConstMetric(descNetBytesSent, prometheus.CounterValue, float64(iface.BytesSent), iface.Name)
			ch <- prometheus.MustNewConstMetric(descNetBytesRecv, prometheus.CounterValue, float64(iface.BytesRecv), iface.Name)
		}
	}
}
