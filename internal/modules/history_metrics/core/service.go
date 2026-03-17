 // Package core provides core interfaces for the history_metrics module.
 // This file contains service interfaces that define contracts for business logic operations
 // related to historical metrics collection, storage, and retrieval.
package core

import (
	"context"
	"time"
)

 // HistoricalMetricsService provides a high-level interface for working with historical metrics.
 // This interface defines the contract for collecting, storing, and retrieving
 // system performance metrics including CPU, memory, disk, network statistics.
type HistoricalMetricsService interface {
	 // CollectAndSaveMetrics collects and persists all current system metrics.
	 // This method orchestrates the collection of system metrics,
	 // then saves them to the configured repository and updates the cache.
	CollectAndSaveMetrics(ctx context.Context) error

	 // StartPeriodicCollection begins automatic periodic collection of metrics.
	 // This method starts a background goroutine that collects metrics at regular intervals
	 // and can be stopped using StopPeriodicCollection.
	StartPeriodicCollection(ctx context.Context, interval time.Duration) error

	 // StopPeriodicCollection stops the periodic metric collection process.
	 // This method safely terminates the background collection goroutine and cleans up resources.
	StopPeriodicCollection()
}
