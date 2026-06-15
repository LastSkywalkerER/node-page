import { useEffect, useState } from 'react'
import { Switch } from '@/shared/ui/switch'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select } from '@/shared/ui/select'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { useSettingsConfig, useSaveServer } from './useSettings'

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
  )
}
