import { useState } from 'react'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select } from '@/shared/ui/select'
import { PasswordInput } from '@/shared/ui/password-input'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { useSettingsConfig, useTestDb, useSwitchDb, type DbSwitchBody } from './useSettings'

export function DatabaseTab() {
  const { data: cfg, isLoading } = useSettingsConfig()
  const testDb = useTestDb()
  const switchDb = useSwitchDb()

  const [dbType, setDbType] = useState<'sqlite' | 'postgres'>('postgres')
  const [managed, setManaged] = useState(true)
  const [host, setHost] = useState('db')
  const [port, setPort] = useState('5432')
  const [name, setName] = useState('node_stats')
  const [user, setUser] = useState('node_stats')
  const [password, setPassword] = useState('')
  const [sslmode, setSslmode] = useState('disable')
  const [sqlitePath, setSqlitePath] = useState('stats.db')
  const [ack, setAck] = useState(false)

  const inDocker = cfg ? !cfg.managed_externally : false
  const isExternalPg = dbType === 'postgres' && !(managed && inDocker)

  const buildBody = (): DbSwitchBody => ({
    db_type: dbType,
    db_managed: dbType === 'postgres' && managed && inDocker,
    db_host: host,
    db_port: port,
    db_name: name,
    db_user: user,
    db_password: password,
    db_sslmode: sslmode,
    db_sqlite_path: sqlitePath,
    acknowledge_data_loss: ack,
  })

  const onTest = () => {
    testDb.mutate(
      { db_host: host, db_port: port, db_name: name, db_user: user, db_password: password, db_sslmode: sslmode },
      {
        onSuccess: (r) =>
          r.ok ? toast.success('Connection OK') : toast.error(r.error || 'Connection failed'),
      },
    )
  }

  const onSwitch = () => switchDb.mutate(buildBody())

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="font-display text-lg tracking-wide">Database</CardTitle>
          <CardDescription>
            Current engine:{' '}
            <span className="font-mono text-foreground">{isLoading ? '…' : cfg?.db_type}</span>
          </CardDescription>
        </CardHeader>
        <CardContent className="max-w-md space-y-4 pt-2">
          <Alert variant="destructive">
            <AlertTitle>Switching the database is a full reset</AlertTitle>
            <AlertDescription>
              Only your users and configuration are migrated. Metric history is{' '}
              <strong>not</strong> carried over — it starts from scratch on the new database (or
              migrate it manually).
            </AlertDescription>
          </Alert>

          {cfg?.managed_externally && (
            <Alert>
              <AlertTitle>Managed externally</AlertTitle>
              <AlertDescription>
                Your orchestrator (e.g. dokploy) sets <code>DB_TYPE</code>/<code>DB_DSN</code> in the
                compose, which overrides what this app writes. Switching here exports your users +
                config and triggers a redeploy, but you must also edit the compose (set the new
                engine / remove the old db service) before that redeploy, or it will boot back on the
                old database.
              </AlertDescription>
            </Alert>
          )}

          <div className="space-y-2">
            <Label htmlFor="db-type" className="text-xs text-muted-foreground">
              New engine
            </Label>
            <Select
              id="db-type"
              value={dbType}
              onChange={(e) => setDbType(e.target.value as 'sqlite' | 'postgres')}
              options={[
                { value: 'postgres', label: 'PostgreSQL' },
                { value: 'sqlite', label: 'SQLite' },
              ]}
            />
          </div>

          {dbType === 'sqlite' && (
            <div className="space-y-2">
              <Label htmlFor="sqlite-path" className="text-xs text-muted-foreground">
                Database file path
              </Label>
              <Input
                id="sqlite-path"
                value={sqlitePath}
                onChange={(e) => setSqlitePath(e.target.value)}
                className="h-9 font-mono text-xs"
              />
            </div>
          )}

          {dbType === 'postgres' && (
            <>
              {inDocker && (
                <label className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={managed}
                    onChange={(e) => setManaged(e.target.checked)}
                  />
                  Let node-stats run PostgreSQL for me (managed container)
                </label>
              )}

              {isExternalPg && (
                <div className="space-y-3">
                  <div className="grid grid-cols-2 gap-2">
                    <div className="space-y-1">
                      <Label className="text-xs text-muted-foreground">Host</Label>
                      <Input value={host} onChange={(e) => setHost(e.target.value)} className="h-9" />
                    </div>
                    <div className="space-y-1">
                      <Label className="text-xs text-muted-foreground">Port</Label>
                      <Input value={port} onChange={(e) => setPort(e.target.value)} className="h-9" />
                    </div>
                  </div>
                  <div className="space-y-1">
                    <Label className="text-xs text-muted-foreground">Database</Label>
                    <Input value={name} onChange={(e) => setName(e.target.value)} className="h-9" />
                  </div>
                  <div className="grid grid-cols-2 gap-2">
                    <div className="space-y-1">
                      <Label className="text-xs text-muted-foreground">User</Label>
                      <Input value={user} onChange={(e) => setUser(e.target.value)} className="h-9" />
                    </div>
                    <div className="space-y-1">
                      <Label className="text-xs text-muted-foreground">Password</Label>
                      <PasswordInput
                        value={password}
                        onChange={(e) => setPassword(e.target.value)}
                        className="h-9"
                      />
                    </div>
                  </div>
                  <div className="space-y-1">
                    <Label className="text-xs text-muted-foreground">SSL mode</Label>
                    <Select
                      value={sslmode}
                      onChange={(e) => setSslmode(e.target.value)}
                      options={[
                        { value: 'disable', label: 'disable' },
                        { value: 'require', label: 'require' },
                        { value: 'verify-ca', label: 'verify-ca' },
                        { value: 'verify-full', label: 'verify-full' },
                      ]}
                    />
                  </div>
                  <Button variant="outline" onClick={onTest} disabled={testDb.isPending}>
                    {testDb.isPending ? 'Testing…' : 'Test connection'}
                  </Button>
                </div>
              )}
            </>
          )}

          <label className="flex items-start gap-2 text-xs text-muted-foreground">
            <input
              type="checkbox"
              checked={ack}
              onChange={(e) => setAck(e.target.checked)}
              className="mt-0.5"
            />
            I understand metric history will be lost and the app will restart to apply the new
            database.
          </label>

          <Button variant="destructive" onClick={onSwitch} disabled={!ack || switchDb.isPending}>
            {switchDb.isPending ? 'Applying…' : 'Switch database (full reset)'}
          </Button>
        </CardContent>
      </Card>
    </div>
  )
}
