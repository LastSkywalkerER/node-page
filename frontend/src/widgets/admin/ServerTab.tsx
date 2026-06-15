import { useEffect, useState } from 'react'
import { Switch } from '@/shared/ui/switch'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select } from '@/shared/ui/select'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { useSettingsConfig, useSaveServer, useSavePorts } from './useSettings'

function currentHttpPort(): string {
  if (window.location.port) return window.location.port
  return window.location.protocol === 'https:' ? '443' : '80'
}

function PortsCard() {
  const savePorts = useSavePorts()
  const [httpPort, setHttpPort] = useState(currentHttpPort())
  const [confirm, setConfirm] = useState(false)

  const changed = httpPort !== currentHttpPort()
  const valid = /^\d+$/.test(httpPort) && Number(httpPort) >= 1 && Number(httpPort) <= 65535

  const onSave = () => {
    savePorts.mutate(
      { http_port: httpPort },
      {
        onSuccess: (r) => {
          setConfirm(false)
          // The HTTP port moved — this connection will drop on recreate. Point
          // the operator at the new address (auto-navigate after a short delay
          // when the controller is recreating).
          if (r.restart_pending) {
            const url = `${window.location.protocol}//${window.location.hostname}:${httpPort}${window.location.pathname}`
            setTimeout(() => {
              window.location.href = url
            }, 8000)
          }
        },
      },
    )
  }

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="font-display text-base tracking-wide">Published HTTP port</CardTitle>
        <CardDescription>
          Changing the port restarts the app and drops this connection — you&apos;ll reconnect on the
          new port.
        </CardDescription>
      </CardHeader>
      <CardContent className="max-w-md space-y-3 pt-2">
        <div className="space-y-2">
          <Label htmlFor="http-port" className="text-xs text-muted-foreground">
            HTTP port
          </Label>
          <Input
            id="http-port"
            value={httpPort}
            onChange={(e) => setHttpPort(e.target.value)}
            placeholder="9090"
            className="h-9 font-mono text-xs"
          />
        </div>
        {changed && (
          <label className="flex items-start gap-2 text-xs text-muted-foreground">
            <input
              type="checkbox"
              checked={confirm}
              onChange={(e) => setConfirm(e.target.checked)}
              className="mt-0.5"
            />
            I understand this restarts the app and I&apos;ll reconnect on port {httpPort}.
          </label>
        )}
        <Button
          variant="outline"
          onClick={onSave}
          disabled={!changed || !valid || !confirm || savePorts.isPending}
        >
          {savePorts.isPending ? 'Applying…' : 'Change port'}
        </Button>
      </CardContent>
    </Card>
  )
}

export function ServerTab() {
  const { data: cfg, isLoading } = useSettingsConfig()
  const save = useSaveServer()

  const [ginMode, setGinMode] = useState('release')
  const [debug, setDebug] = useState(false)
  const [hostname, setHostname] = useState('')
  const [ipv4, setIpv4] = useState('')
  const [addr, setAddr] = useState('')

  useEffect(() => {
    if (!cfg) return
    setGinMode(cfg.gin_mode || 'release')
    setDebug(cfg.debug === 'true')
    setHostname(cfg.node_stats_hostname || '')
    setIpv4(cfg.node_stats_ipv4 || '')
    setAddr(cfg.addr || '')
  }, [cfg])

  // In Docker the listen address is fixed by the generated compose and the
  // published port lives in the installer-owned stack .env, so editing ADDR
  // here is native-only.
  const managed = cfg?.managed_externally
  const addrEditable = cfg ? !managed : false

  const onSave = () => {
    save.mutate({
      gin_mode: ginMode,
      debug,
      hostname,
      ipv4,
      ...(addrEditable ? { addr } : {}),
    })
  }

  return (
    <div className="space-y-6">
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="font-display text-lg tracking-wide">Server</CardTitle>
        <CardDescription>
          Identity and runtime options. Changes apply after the app restarts.
        </CardDescription>
      </CardHeader>
      <CardContent className="max-w-md space-y-4 pt-2">
        <div className="space-y-2">
          <Label htmlFor="gin-mode" className="text-xs text-muted-foreground">
            Mode
          </Label>
          <Select
            id="gin-mode"
            value={ginMode}
            onChange={(e) => setGinMode(e.target.value)}
            options={[
              { value: 'release', label: 'release' },
              { value: 'debug', label: 'debug' },
            ]}
          />
        </div>

        <div className="flex items-center justify-between gap-4">
          <div>
            <Label className="text-sm">Debug logging</Label>
            <p className="text-xs text-muted-foreground">Verbose logs — keep off in production.</p>
          </div>
          <Switch checked={debug} onCheckedChange={setDebug} disabled={isLoading} />
        </div>

        <div className="space-y-2">
          <Label htmlFor="hostname" className="text-xs text-muted-foreground">
            Display hostname (optional)
          </Label>
          <Input
            id="hostname"
            value={hostname}
            onChange={(e) => setHostname(e.target.value)}
            placeholder="overrides the card / breadcrumb label"
            className="h-9"
          />
        </div>

        <div className="space-y-2">
          <Label htmlFor="ipv4" className="text-xs text-muted-foreground">
            IPv4 override (optional)
          </Label>
          <Input
            id="ipv4"
            value={ipv4}
            onChange={(e) => setIpv4(e.target.value)}
            placeholder="leave blank to auto-detect"
            className="h-9"
          />
        </div>

        {addrEditable ? (
          <div className="space-y-2">
            <Label htmlFor="addr" className="text-xs text-muted-foreground">
              Listen address
            </Label>
            <Input
              id="addr"
              value={addr}
              onChange={(e) => setAddr(e.target.value)}
              placeholder=":8080"
              className="h-9 font-mono text-xs"
            />
            <p className="text-xs text-muted-foreground">
              Changing the port restarts the app — reconnect on the new address afterwards.
            </p>
          </div>
        ) : (
          <p className="text-xs text-muted-foreground">
            The listen port is managed by your deployment (compose / installer), so it can&apos;t be
            changed here.
          </p>
        )}

        <Button onClick={onSave} disabled={save.isPending || isLoading}>
          {save.isPending ? 'Saving…' : 'Save'}
        </Button>
      </CardContent>
    </Card>
    {!managed && <PortsCard />}
    </div>
  )
}
