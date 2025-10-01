import { useDashboardStore } from '@/shared/lib/store';
import type { DashboardFilters, TimeRange } from '@/shared/types/metrics';
import { Button } from '@/shared/ui/button';
import { Badge } from '@/shared/ui/badge';

interface FiltersBarProps {
  filters: DashboardFilters;
}

const timeRanges: { value: TimeRange; label: string }[] = [
  { value: '5m', label: '5m' },
  { value: '1h', label: '1h' },
  { value: '24h', label: '24h' },
  { value: '7d', label: '7d' },
];

export default function FiltersBar({ filters }: FiltersBarProps) {
  const setFilters = useDashboardStore((state) => state.setFilters);

  return (
    <div className="flex items-center space-x-4">
      {/* Time Range Selector */}
      <div className="flex rounded-lg bg-white/5 p-1">
        {timeRanges.map((range) => (
          <Button
            key={range.value}
            variant={filters.timeRange === range.value ? 'default' : 'ghost'}
            size="sm"
            onClick={() => setFilters({ timeRange: range.value })}
            className="px-3 py-1 text-xs"
          >
            {range.label}
          </Button>
        ))}
      </div>

      {/* Filter Toggles */}
      <div className="flex items-center space-x-2">
        <Badge
          variant={filters.showSystem ? 'default' : 'secondary'}
          className="cursor-pointer"
          onClick={() => setFilters({ showSystem: !filters.showSystem })}
        >
          System
        </Badge>
        <Badge
          variant={filters.showDocker ? 'default' : 'secondary'}
          className="cursor-pointer"
          onClick={() => setFilters({ showDocker: !filters.showDocker })}
        >
          Docker
        </Badge>
        <Badge
          variant={filters.showNetwork ? 'default' : 'secondary'}
          className="cursor-pointer"
          onClick={() => setFilters({ showNetwork: !filters.showNetwork })}
        >
          Network
        </Badge>
      </div>
    </div>
  );
}
