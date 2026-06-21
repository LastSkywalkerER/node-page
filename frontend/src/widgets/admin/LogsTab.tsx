import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { apiClient } from '@/shared/lib/api'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { RefreshCw, Pause, Play } from 'lucide-react'
import { cn } from '@/lib/utils'
import { LogViewer } from '@/widgets/applications/LogViewer'

interface LogsResponse {
  logs: string
  lines: number
}

/**
 * LogsTab surfaces this node's own recent process logs (an in-memory ring on
 * the backend) so admins can read them in the UI — no shell / `docker logs` /
 * journalctl needed, which matters most for native installs. Tails by polling
 * every 5s; the operator can pause to read scrollback.
 */
export function LogsTab() {
  const [live, setLive] = useState(true)

  const { data, isLoading, isFetching, refetch } = useQuery({
    queryKey: ['app-logs'],
    queryFn: async () => {
      const res = await apiClient.get<LogsResponse>('/logs')
      return res.data
    },
    refetchInterval: live ? 5000 : false,
  })

  return (
    <Card>
      <CardHeader className="pb-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <CardTitle className="font-display text-lg tracking-wide">Logs</CardTitle>
            <CardDescription>
              This node&apos;s recent application logs (kept in memory, newest at the bottom).
            </CardDescription>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => setLive((v) => !v)}
              className="gap-1.5"
            >
              {live ? <Pause className="h-3.5 w-3.5" /> : <Play className="h-3.5 w-3.5" />}
              {live ? 'Pause' : 'Live'}
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => refetch()}
              disabled={isFetching}
              className="gap-1.5"
            >
              <RefreshCw className={cn('h-3.5 w-3.5', isFetching && 'animate-spin')} />
              Refresh
            </Button>
          </div>
        </div>
      </CardHeader>
      <CardContent className="pt-0">
        <LogViewer logs={data?.logs ?? ''} loading={isLoading} />
      </CardContent>
    </Card>
  )
}
