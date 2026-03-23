import type { LucideIcon } from 'lucide-react'
import { Card, CardContent, CardHeader } from '@/components/ui/card'

const DEFAULT_MESSAGE =
  'No metrics for this host yet. Data appears after collectors persist snapshots for this host_id in the database.'

interface MetricWidgetEmptyProps {
  icon: LucideIcon
  label: string
  message?: string
}

/** Shown when API returns latest: null for a host (e.g. remote cluster node). */
export function MetricWidgetEmpty({ icon: Icon, label, message = DEFAULT_MESSAGE }: MetricWidgetEmptyProps) {
  return (
    <Card>
      <CardHeader className="pb-3">
        <div className="flex items-center gap-2">
          <Icon className="h-4 w-4 text-muted-foreground" />
          <span className="text-sm font-medium text-muted-foreground">{label}</span>
        </div>
      </CardHeader>
      <CardContent className="pt-0">
        <p className="text-xs text-muted-foreground leading-relaxed">{message}</p>
      </CardContent>
    </Card>
  )
}
