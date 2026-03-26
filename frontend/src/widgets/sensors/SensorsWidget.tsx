import { Thermometer } from 'lucide-react'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { MetricCardSkeleton } from '@/shared/components/MetricCardSkeleton'
import { MetricWidgetEmpty } from '@/shared/components/MetricWidgetEmpty'
import { useSensors } from './useSensors'
import { TemperatureStat } from './schemas'

interface SensorsWidgetProps { hostId: number }

function tempColor(temp: number, high?: number | null, critical?: number | null): string {
  if (critical && temp >= critical * 0.95) return '#ef4444'
  if (high && temp >= high * 0.9) return '#f59e0b'
  if (temp >= 80) return '#ef4444'
  if (temp >= 65) return '#f59e0b'
  return '#22c55e'
}

export default function SensorsWidget({ hostId }: SensorsWidgetProps) {
  const { data, isLoading } = useSensors(hostId)
  if (isLoading || !data) return <MetricCardSkeleton />

  if (data?.sensors == null) {
    return (
      <MetricWidgetEmpty
        icon={Thermometer}
        label="Sensors"
        message="No sensor data for this host. Temperatures are only collected on the machine running this server (usually Linux)."
      />
    )
  }

  const sensorsSorted: TemperatureStat[] = [...(data?.sensors?.sensors ?? [])].sort(
    (a, b) => b.temperature - a.temperature
  )
  const visible = sensorsSorted.slice(0, 8)
  const hottest = sensorsSorted[0] ?? null
  const hottestColor = hottest ? tempColor(hottest.temperature, hottest.high, hottest.critical) : '#22c55e'

  return (
    <Card>
      <CardHeader className="pb-3">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Thermometer className="h-4 w-4 text-muted-foreground" />
            <span className="text-sm font-medium text-muted-foreground">Sensors</span>
          </div>
          <span className="text-2xl font-bold tabular-nums" style={{ color: hottestColor }}>
            {hottest ? `${hottest.temperature.toFixed(1)}°C` : 'N/A'}
          </span>
        </div>
      </CardHeader>
      <CardContent className="pt-0">
        {sensorsSorted.length === 0 ? (
          <p className="text-xs text-muted-foreground">No sensor data (Linux only)</p>
        ) : (
          <div className="space-y-1">
            {visible.map(s => {
              const color = tempColor(s.temperature, s.high, s.critical)
              return (
                <div key={s.sensor_key} className="flex justify-between text-xs">
                  <span className="text-muted-foreground truncate max-w-[55%]">{s.sensor_key}</span>
                  <span style={{ color }} className="font-medium tabular-nums">
                    {s.temperature.toFixed(1)}°C
                    {s.high ? <span className="text-muted-foreground font-normal"> / {s.high.toFixed(0)}°</span> : null}
                  </span>
                </div>
              )
            })}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
