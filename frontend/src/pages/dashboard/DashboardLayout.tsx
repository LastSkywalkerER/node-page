import { useLayoutTheme } from '@/shared/themes';
import {
  CPUWidget,
  MemoryWidget,
  DiskWidget,
  NetworkWidget,
  DockerWidget,
} from '@/widgets';

export function DashboardLayout() {
  const layoutTheme = useLayoutTheme();

  return (
    <div className={layoutTheme.mainContainer.className}>

      {/* Widgets Layout */}
      <div className="grid grid-cols-12 gap-4 md:gap-6">
        {/* Left Column - Metrics Widgets */}
        <div className="col-span-12 lg:col-span-4 xl:col-span-3 space-y-4 md:space-y-6">
          {/* CPU Widget */}
          <div className={layoutTheme.card.className}>
            <CPUWidget />
          </div>

          {/* Memory Widget */}
          <div className={layoutTheme.card.className}>
            <MemoryWidget />
          </div>

          {/* Disk Widget */}
          <div className={layoutTheme.card.className}>
            <DiskWidget />
          </div>

          {/* Network Widget */}
          <div className={layoutTheme.card.className}>
            <NetworkWidget />
          </div>
        </div>

        {/* Right Column - Docker Widget */}
        <div className="col-span-12 lg:col-span-8 xl:col-span-9">
          <div className={layoutTheme.card.className}>
            <DockerWidget />
          </div>
        </div>
      </div>
    </div>
  );
}
