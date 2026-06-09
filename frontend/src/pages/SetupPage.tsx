import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import {
  useSetupStatus,
  useSetupConfig,
  useCompleteSetup,
  useSetupEnvPreview,
  useJoinRaftCluster,
} from '../widgets/setup/useSetup'
import { SetupConfigFormData, AdminUserFormData } from '../widgets/setup/schemas'
import {
  WelcomeWidget, WELCOME_STEP_META,
  ConfigFormWidget, CONFIG_STEP_META,
  AdminFormWidget, ADMIN_STEP_META,
  ReviewWidget, REVIEW_STEP_META,
  SuccessWidget, SUCCESS_STEP_META,
  JoinClusterWidget, JOIN_CLUSTER_STEP_META,
} from '../widgets/setup'
import { DEFAULT_SETUP_CONFIG } from '../shared/config/setup'

// extractPort returns the port portion of an "addr" like ":7000" or
// "192.168.0.104:7000". Empty string when input doesn't have a port.
function extractPort(addr?: string): string {
  if (!addr) return ''
  const m = addr.match(/:(\d+)$/)
  return m ? m[1] : ''
}

// extractHost returns the host portion of an "addr" like "192.168.0.104:7000".
// Empty string when addr is empty, ":port"-style, or unparseable.
function extractHost(addr?: string): string {
  if (!addr) return ''
  const idx = addr.lastIndexOf(':')
  if (idx <= 0) return ''
  return addr.slice(0, idx)
}

type Step = 'welcome' | 'config' | 'admin' | 'review' | 'applying' | 'success' | 'join'

const STEP_META = {
  welcome: WELCOME_STEP_META,
  config: CONFIG_STEP_META,
  admin: ADMIN_STEP_META,
  review: REVIEW_STEP_META,
  applying: { title: 'Applying configuration', description: 'Provisioning the database and restarting the stack…' },
  success: SUCCESS_STEP_META,
  join: JOIN_CLUSTER_STEP_META,
} as const

export function SetupPage() {
  const navigate = useNavigate()
  const [step, setStep] = useState<Step>('welcome')
  const [configData, setConfigData] = useState<SetupConfigFormData | null>(null)
  const [adminData, setAdminData] = useState<AdminUserFormData | null>(null)
  // joinStatus = 'idle' before submission, 'joining' while the POST is in
  // flight, 'replicating' after a successful join — we then poll setup
  // status until the cluster snapshot lands (setup_needed flips false).
  const [joinStatus, setJoinStatus] = useState<'idle' | 'joining' | 'replicating'>('idle')

  const { data: statusData, isLoading: statusLoading } = useSetupStatus()
  const { data: configResponse, isLoading: configLoading } = useSetupConfig({
    enabled: statusData?.setup_needed === true,
  })
  const completeSetup = useCompleteSetup()
  const envPreview = useSetupEnvPreview(configData, step === 'review' && configData !== null)
  const joinCluster = useJoinRaftCluster()

  const getInitialConfigValues = (): Partial<SetupConfigFormData> => {
    if (configResponse?.config) {
      const c = configResponse.config
      return {
        jwt_secret: c.jwt_secret || DEFAULT_SETUP_CONFIG.jwt_secret,
        refresh_secret: c.refresh_secret || DEFAULT_SETUP_CONFIG.refresh_secret,
        addr: c.addr || DEFAULT_SETUP_CONFIG.addr,
        gin_mode: (c.gin_mode === 'debug' || c.gin_mode === 'release')
          ? c.gin_mode : DEFAULT_SETUP_CONFIG.gin_mode,
        debug: (c.debug === 'true' || c.debug === 'false')
          ? c.debug : DEFAULT_SETUP_CONFIG.debug,
        db_type: c.db_type === 'postgres' ? 'postgres' : 'sqlite',
        db_sqlite_path: c.db_type === 'sqlite' ? (c.db_dsn || DEFAULT_SETUP_CONFIG.db_sqlite_path) : DEFAULT_SETUP_CONFIG.db_sqlite_path,
        prometheus_enabled: (c.prometheus_enabled === 'true' || c.prometheus_enabled === 'false')
          ? c.prometheus_enabled : DEFAULT_SETUP_CONFIG.prometheus_enabled,
        prometheus_auth: (c.prometheus_auth === 'true' || c.prometheus_auth === 'false')
          ? c.prometheus_auth : DEFAULT_SETUP_CONFIG.prometheus_auth,
        prometheus_token: c.prometheus_token || DEFAULT_SETUP_CONFIG.prometheus_token,
        docker_host_metrics_compat: typeof c.docker_host_metrics_compat === 'boolean'
          ? c.docker_host_metrics_compat
          : DEFAULT_SETUP_CONFIG.docker_host_metrics_compat,
        node_stats_hostname: c.node_stats_hostname ?? DEFAULT_SETUP_CONFIG.node_stats_hostname,
        node_stats_ipv4: c.node_stats_ipv4 ?? DEFAULT_SETUP_CONFIG.node_stats_ipv4,
        // Prefer values from the existing .env over hard-coded defaults so a
        // retry after a failed activation shows the same fields the
        // operator saw last time (e.g. the port that didn't bind).
        raft_enabled:
          c.raft_enabled === 'true' || c.raft_enabled === 'false'
            ? c.raft_enabled
            : DEFAULT_SETUP_CONFIG.raft_enabled,
        raft_cluster_id: c.raft_cluster_id ?? DEFAULT_SETUP_CONFIG.raft_cluster_id,
        raft_node_id: c.raft_node_id ?? DEFAULT_SETUP_CONFIG.raft_node_id,
        // Derive single port + host from the persisted RAFT_BIND_ADDR /
        // RAFT_ADVERTISE_ADDR pair so a wizard retry shows the same
        // values without forcing the operator to think about which one
        // is which.
        raft_port: extractPort(c.raft_bind_addr || c.raft_advertise_addr) || DEFAULT_SETUP_CONFIG.raft_port,
        raft_advertise_host: extractHost(c.raft_advertise_addr) || DEFAULT_SETUP_CONFIG.raft_advertise_host,
        raft_data_dir: c.raft_data_dir ?? DEFAULT_SETUP_CONFIG.raft_data_dir,
        raft_bootstrap:
          c.raft_bootstrap === 'true' || c.raft_bootstrap === 'false'
            ? c.raft_bootstrap
            : DEFAULT_SETUP_CONFIG.raft_bootstrap,
        raft_advertise_public_url:
          c.raft_advertise_public_url ?? DEFAULT_SETUP_CONFIG.raft_advertise_public_url,
        raft_bridge_enabled:
          c.raft_bridge_enabled === 'true' || c.raft_bridge_enabled === 'false'
            ? c.raft_bridge_enabled
            : DEFAULT_SETUP_CONFIG.raft_bridge_enabled,
        raft_bridge_shared_secret:
          c.raft_bridge_shared_secret ?? DEFAULT_SETUP_CONFIG.raft_bridge_shared_secret,
        raft_bridge_remote_seeds:
          c.raft_bridge_remote_seeds ?? DEFAULT_SETUP_CONFIG.raft_bridge_remote_seeds,
      }
    }
    return {}
  }

  useEffect(() => {
    if (statusData && !statusData.setup_needed) navigate('/auth')
  }, [statusData, navigate])

  // While replicating, refetch setup-status every 2s until it flips and the
  // effect above redirects. This is what makes the join flow feel "zero
  // configuration": the user pastes a token, and a few seconds later the
  // page redirects to login.
  useEffect(() => {
    if (joinStatus !== 'replicating') return
    const id = window.setInterval(() => {
      // refetch is triggered by invalidating the query key.
      // The `useSetupStatus` hook will pick up the new status.
      // (Tanstack Query exposes `queryClient.invalidateQueries`, but
      // invalidating via window.dispatchEvent is overkill here — we
      // simply rely on the polling interval below.)
      void fetch('/api/v1/setup/status', { credentials: 'include' })
        .then((r) => r.ok && r.json())
        .then((j) => {
          if (j?.data?.setup_needed === false) navigate('/auth')
        })
        .catch(() => {})
    }, 2000)
    return () => window.clearInterval(id)
  }, [joinStatus, navigate])

  const handleCompleteSetup = async () => {
    if (!configData || !adminData) return
    try {
      const res = await completeSetup.mutateAsync({
        config: configData,
        admin_email: adminData.email,
        admin_password: adminData.password,
      })
      // A DB-engine switch (Docker) recreates the stack before the admin can be
      // created on the new database; show the "applying" gate which waits for the
      // backend to come back and re-submits to create the admin there.
      setStep(res.restart_pending ? 'applying' : 'success')
    } catch (error) {
      console.error('Setup completion error:', error)
    }
  }

  // Applying gate: after a DB switch the controller recreates the app onto the
  // new (empty) database. Poll until it answers again, then re-submit to create
  // the admin on the new DB. Tolerate connection drops during the restart.
  useEffect(() => {
    if (step !== 'applying' || !configData || !adminData) return
    let resubmitting = false
    let done = false
    const id = window.setInterval(async () => {
      if (done) return
      // /version returns only once the recreated backend is fully up.
      const up = await fetch('/api/v1/version').then((r) => r.ok).catch(() => false)
      if (!up) return
      const status = await fetch('/api/v1/setup/status', { credentials: 'include' })
        .then((r) => (r.ok ? r.json() : null))
        .catch(() => null)
      if (!status?.data) return
      if (status.data.setup_needed === false) {
        done = true
        window.clearInterval(id)
        navigate('/auth')
        return
      }
      if (!resubmitting) {
        resubmitting = true
        try {
          const r2 = await completeSetup.mutateAsync({
            config: configData,
            admin_email: adminData.email,
            admin_password: adminData.password,
          })
          if (!r2.restart_pending) {
            done = true
            window.clearInterval(id)
            setStep('success')
          } else {
            resubmitting = false // another switch pending; keep polling
          }
        } catch {
          resubmitting = false // transient (backend mid-restart); retry next tick
        }
      }
    }, 2000)
    return () => window.clearInterval(id)
    // completeSetup is a stable mutation; intentionally excluded from deps.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [step, configData, adminData, navigate])

  const handleJoinCluster = async (values: {
    peer_url: string
    token: string
    advertise_url: string
  }) => {
    setJoinStatus('joining')
    try {
      // Everything except the cluster node URL, connect key and (optional)
      // public URL override is auto-derived on the backend: node_id from
      // hostname, bind addr :7000, advertise from the detected IP, cluster
      // id probed from the peer.
      await joinCluster.mutateAsync({
        peer_url: values.peer_url,
        token: values.token,
        advertise_url: values.advertise_url || undefined,
      })
      setJoinStatus('replicating')
    } catch (err) {
      console.error('Join cluster error:', err)
      setJoinStatus('idle')
    }
  }

  if (statusLoading || configLoading) {
    return (
      <div className="app-shell app-shell--fill relative flex min-h-0 flex-1 items-center justify-center">
        <div className="app-shell-content">
          <p className="text-muted-foreground text-sm font-mono">Loading...</p>
        </div>
      </div>
    )
  }

  return (
    <div className="app-shell app-shell--fill relative flex min-h-0 flex-1 items-center justify-center p-4">
      <div className="app-shell-content w-full max-w-2xl">
        <div className="text-center mb-8">
          <h1 className="text-3xl font-bold font-display tracking-wide mb-1 uppercase">node-stats</h1>
          <p className="text-muted-foreground text-xs tracking-[0.25em] uppercase">Initial setup</p>
        </div>
        <Card>
          <CardHeader>
            <CardTitle>{STEP_META[step].title}</CardTitle>
            <CardDescription>{STEP_META[step].description}</CardDescription>
          </CardHeader>
          <CardContent>
            {step === 'welcome' && (
              <WelcomeWidget
                onStartNewCluster={() => setStep('config')}
                onJoinExistingCluster={() => setStep('join')}
              />
            )}
            {step === 'join' && (
              <JoinClusterWidget
                isJoining={joinCluster.isPending}
                status={joinStatus}
                error={
                  joinCluster.error
                    ? (joinCluster.error as Error).message
                    : null
                }
                onBack={() => setStep('welcome')}
                onSubmit={handleJoinCluster}
              />
            )}
            {step === 'config' && (
              <ConfigFormWidget
                initialValues={getInitialConfigValues()}
                runningInDocker={statusData?.running_in_docker === true}
                machineHints={statusData?.machine_hints ?? { suggested_hostname: '', suggested_ipv4: '' }}
                onSubmit={(data) => { setConfigData(data); setStep('admin') }}
                onBack={() => setStep('welcome')}
              />
            )}
            {step === 'admin' && (
              <AdminFormWidget
                onSubmit={(data) => { setAdminData(data); setStep('review') }}
                onBack={() => setStep('config')}
              />
            )}
            {step === 'review' && configData && adminData && (
              <ReviewWidget
                adminData={adminData}
                config={configData}
                envContent={envPreview.data}
                envLoading={envPreview.isLoading}
                envError={envPreview.error}
                onBack={() => setStep('admin')}
                onComplete={handleCompleteSetup}
                isCompleting={completeSetup.isPending}
                error={completeSetup.error}
              />
            )}
            {step === 'applying' && (
              <div className="flex flex-col items-center gap-4 py-8 text-center">
                <div className="h-8 w-8 animate-spin rounded-full border-2 border-slate-600 border-t-sky-400" />
                <p className="text-sm text-slate-300">Provisioning the database and restarting the stack…</p>
                <p className="text-xs text-slate-500">
                  This can take a moment — the backend is being recreated on the new database.
                  You'll be redirected to sign in automatically.
                </p>
              </div>
            )}
            {step === 'success' && (
              <SuccessWidget
                raftEnabled={configData?.raft_enabled === 'true'}
                adminEmail={adminData?.email}
                adminPassword={adminData?.password}
              />
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
