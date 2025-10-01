import { type ClassValue, clsx } from 'clsx';
import { twMerge } from 'tailwind-merge';

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

// Format bytes to human readable format
export function formatBytes(bytes: number): string {
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let value = bytes;
  let unitIndex = 0;

  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex++;
  }

  return `${value.toFixed(1)} ${units[unitIndex]}`;
}

// Format network speed
export function formatNetworkSpeed(bytesPerSecond: number): string {
  const units = ['B/s', 'KB/s', 'MB/s', 'GB/s'];
  let value = bytesPerSecond;
  let unitIndex = 0;

  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex++;
  }

  return `${value.toFixed(1)} ${units[unitIndex]}`;
}

// Format percentage
export function formatPercentage(value: number): string {
  return `${value.toFixed(1)}%`;
}

// Format uptime
export function formatUptime(seconds: number): string {
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);

  if (days > 0) {
    return `${days}d ${hours}h ${minutes}m`;
  } else if (hours > 0) {
    return `${hours}h ${minutes}m`;
  } else {
    return `${minutes}m`;
  }
}

// Get color for CPU usage
export function getCPUColor(usage: number): string {
  if (usage < 50) return '#22c55e'; // green
  if (usage < 80) return '#f59e0b'; // yellow
  return '#ef4444'; // red
}

// Get color for memory usage
export function getMemoryColor(usage: number): string {
  if (usage < 70) return '#7c3aed'; // purple
  if (usage < 90) return '#f59e0b'; // yellow
  return '#ef4444'; // red
}

// Get color for disk usage
export function getDiskColor(usage: number): string {
  if (usage < 80) return '#f59e0b'; // yellow
  return '#ef4444'; // red
}

// Get container state color
export function getContainerStateColor(state: string): string {
  switch (state.toLowerCase()) {
    case 'running':
      return '#22c55e';
    case 'exited':
    case 'stopped':
      return '#6b7280';
    case 'restarting':
      return '#f59e0b';
    case 'paused':
      return '#3b82f6';
    default:
      return '#6b7280';
  }
}

// Calculate health score (0-100)
export function calculateHealthScore(metrics: {
  cpu: { usage_percent: number };
  memory: { usage_percent: number };
  disk: { usage_percent: number };
}): number {
  const cpuScore = Math.max(0, 100 - metrics.cpu.usage_percent * 0.8);
  const memoryScore = Math.max(0, 100 - metrics.memory.usage_percent * 0.9);
  const diskScore = Math.max(0, 100 - metrics.disk.usage_percent);

  return Math.round((cpuScore + memoryScore + diskScore) / 3);
}

// Debounce function
export function debounce<T extends (...args: any[]) => any>(
  func: T,
  wait: number
): (...args: Parameters<T>) => void {
  let timeout: NodeJS.Timeout;
  return (...args: Parameters<T>) => {
    clearTimeout(timeout);
    timeout = setTimeout(() => func(...args), wait);
  };
}

// Get time range in seconds
export function getTimeRangeSeconds(range: string): number {
  switch (range) {
    case '5m':
      return 300;
    case '1h':
      return 3600;
    case '24h':
      return 86400;
    case '7d':
      return 604800;
    default:
      return 3600;
  }
}

