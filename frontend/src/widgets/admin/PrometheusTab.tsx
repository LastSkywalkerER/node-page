import { useEffect, useState } from 'react'
import { Switch } from '@/shared/ui/switch'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { useSettingsConfig, useSavePrometheus } from './useSettings'

function generateToken(): string {
  const bytes = new Uint8Array(24)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
}

export function PrometheusTab() {
  const { data: cfg, isLoading } = useSettingsConfig()
  const save = useSavePrometheus()

  const [enabled, setEnabled] = useState(false)
  const [auth, setAuth] = useState(false)
  const [token, setToken] = useState('')
  // Whether a token is already stored server-side (we never receive its value).
  const [tokenStored, setTokenStored] = useState(false)

  useEffect(() => {
    if (!cfg) return
    setEnabled(cfg.prometheus_enabled === 'true')
    setAuth(cfg.prometheus_auth === 'true')
    setTokenStored(cfg.prometheus_token_set)
    setToken('')
  }, [cfg])

  const needsToken = enabled && auth && !tokenStored && token.trim() === ''

  const onSave = () => {
    save.mutate({
      enabled,
      auth,
      // Only send a token when the user typed one; an empty string keeps the
      // stored token (backend leaves it untouched unless a value is provided).
      token: token.trim() === '' ? undefined : token.trim(),
    })
  }

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="font-display text-lg tracking-wide">Prometheus</CardTitle>
        <CardDescription>
          Expose <span className="font-mono">/api/v1/metrics</span> for scraping. Changes apply after
          the app restarts.
        </CardDescription>
      </CardHeader>
      <CardContent className="max-w-md space-y-4 pt-2">
        <div className="flex items-center justify-between gap-4">
          <div>
            <Label className="text-sm">Enable metrics endpoint</Label>
            <p className="text-xs text-muted-foreground">Serves Prometheus metrics for scraping.</p>
          </div>
          <Switch checked={enabled} onCheckedChange={setEnabled} disabled={isLoading} />
        </div>

        {enabled && (
          <div className="flex items-center justify-between gap-4">
            <div>
              <Label className="text-sm">Require bearer token</Label>
              <p className="text-xs text-muted-foreground">Protect the endpoint with a static token.</p>
            </div>
            <Switch checked={auth} onCheckedChange={setAuth} />
          </div>
        )}

        {enabled && auth && (
          <div className="space-y-2">
            <Label htmlFor="prom-token" className="text-xs text-muted-foreground">
              Bearer token {tokenStored && <span className="text-emerald-500">(one is set)</span>}
            </Label>
            <div className="flex flex-col gap-2 sm:flex-row">
              <Input
                id="prom-token"
                placeholder={tokenStored ? 'Leave blank to keep current token' : 'Paste or generate'}
                value={token}
                onChange={(e) => setToken(e.target.value)}
                className="h-9 font-mono text-xs sm:flex-1"
              />
              <Button
                type="button"
                variant="outline"
                className="h-9 shrink-0"
                onClick={() => setToken(generateToken())}
              >
                Generate
              </Button>
            </div>
          </div>
        )}

        <Button onClick={onSave} disabled={save.isPending || isLoading || needsToken}>
          {save.isPending ? 'Saving…' : 'Save'}
        </Button>
      </CardContent>
    </Card>
  )
}
