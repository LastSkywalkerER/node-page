import { useLayoutTheme } from '@/shared/themes';
import { useHost } from '@/shared/lib/HostContext';
import {
  CPUWidget,
  MemoryWidget,
  DiskWidget,
  NetworkWidget,
  DockerWidget,
} from '@/widgets';

export function DashboardLayout() {
  const layoutTheme = useLayoutTheme();
  const { selectedHostId } = useHost();

  return (
    <div className={layoutTheme.mainContainer.className}>

      {/* Widgets Layout */}
      <div className="grid grid-cols-12 gap-4 md:gap-6">
        {/* Left Column - Metrics Widgets */}
        <div className="col-span-12 lg:col-span-4 xl:col-span-3 space-y-4 md:space-y-6">
          {/* CPU Widget */}
          <div className={layoutTheme.card.className}>
            <CPUWidget hostId={selectedHostId} />
          </div>

          {/* Memory Widget */}
          <div className={layoutTheme.card.className}>
            <MemoryWidget hostId={selectedHostId} />
          </div>

          {/* Disk Widget */}
          <div className={layoutTheme.card.className}>
            <DiskWidget hostId={selectedHostId} />
          </div>

          {/* Network Widget */}
          <div className={layoutTheme.card.className}>
            <NetworkWidget hostId={selectedHostId} />
          </div>
        </div>

        {/* Right Column - Docker Widget */}
        <div className="col-span-12 lg:col-span-8 xl:col-span-9">
          <div className={layoutTheme.card.className}>
            <DockerWidget hostId={selectedHostId} />
          </div>
        </div>
      </div>
    </div>
  );
}
