import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'

interface MetricCardSkeletonProps {
  title?: string
}

export function MetricCardSkeleton({ title }: MetricCardSkeletonProps) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Skeleton className="h-4 w-4 rounded" />
            <Skeleton className="h-4 w-16" />
          </div>
          <Skeleton className="h-7 w-14" />
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        <Skeleton className="h-2 w-full rounded-full" />
        <div className="space-y-1.5">
          {[1, 2, 3].map(i => (
            <div key={i} className="flex justify-between">
              <Skeleton className="h-3 w-20" />
              <Skeleton className="h-3 w-16" />
            </div>
          ))}
        </div>
        <Skeleton className="h-24 w-full rounded" />
      </CardContent>
    </Card>
  )
}
