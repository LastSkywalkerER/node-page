package collectors

import (
	"context"

	"github.com/charmbracelet/log"
	"github.com/shirou/gopsutil/v3/net"

	"system-stats/internal/modules/network/infrastructure/entities"
)

/**
 * networkMetricsCollector implements the NetworkMetricsCollector interface.
 * This collector gathers network performance statistics using cross-platform
 * system monitoring libraries (gopsutil).
 */
type NetworkMetricsCollector struct {
	logger *log.Logger
}

/**
 * NewNetworkMetricsCollector creates a new network metrics collector instance.
 * This constructor initializes the collector for gathering network statistics.
 *
 * @param logger The logger instance for logging collection operations
 * @return *networkMetricsCollector Returns the initialized network collector
 */
func NewNetworkMetricsCollector(logger *log.Logger) *NetworkMetricsCollector {
	return &NetworkMetricsCollector{logger: logger}
}

/**
 * CollectNetworkMetrics gathers current network performance statistics.
 * This method collects network interface statistics including bytes sent/received
 * and packet counts, excluding loopback interfaces.
 *
 * @param ctx The context for the operation
 * @return entities.NetworkMetric The collected network metrics
 * @return error Returns an error if network metrics collection fails
 */
func (c *NetworkMetricsCollector) CollectNetworkMetrics(ctx context.Context) (entities.NetworkMetric, error) {
	c.logger.Info("Collecting network interface statistics")
	netStats, err := net.IOCountersWithContext(ctx, true)
	if err != nil {
		c.logger.Error("Failed to collect network interface statistics", "error", err)
		return entities.NetworkMetric{}, err
	}

	interfaces := make([]entities.NetworkInterface, 0, len(netStats))
	for _, stat := range netStats {
		// Skip loopback interfaces
		if stat.Name == "lo" || stat.Name == "lo0" {
			continue
		}

		interfaces = append(interfaces, entities.NetworkInterface{
			Name:        stat.Name,
			BytesSent:   stat.BytesSent,
			BytesRecv:   stat.BytesRecv,
			PacketsSent: stat.PacketsSent,
			PacketsRecv: stat.PacketsRecv,
		})
	}

	c.logger.Info("Network metrics collected successfully", "interfaces_count", len(interfaces))
	return entities.NetworkMetric{
		Interfaces: interfaces,
	}, nil
}
