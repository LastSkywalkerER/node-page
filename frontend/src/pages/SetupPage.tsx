import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { useSetupStatus, useSetupConfig, useCompleteSetup, useSetupEnvPreview } from '../widgets/setup/useSetup'
import { SetupConfigFormData, AdminUserFormData } from '../widgets/setup/schemas'
import {
  WelcomeWidget, WELCOME_STEP_META,
  ConfigFormWidget, CONFIG_STEP_META,
  AdminFormWidget, ADMIN_STEP_META,
  ReviewWidget, REVIEW_STEP_META,
  SuccessWidget, SUCCESS_STEP_META,
} from '../widgets/setup'
import { DEFAULT_SETUP_CONFIG } from '../shared/config/setup'
type Step = 'welcome' | 'config' | 'admin' | 'review' | 'success'

const STEP_META = {
  welcome: WELCOME_STEP_META,
  config: CONFIG_STEP_META,
  admin: ADMIN_STEP_META,
  review: REVIEW_STEP_META,
  success: SUCCESS_STEP_META,
} as const

export function SetupPage() {
  const navigate = useNavigate()
  const [step, setStep] = useState<Step>('welcome')
  const [configData, setConfigData] = useState<SetupConfigFormData | null>(null)
  const [adminData, setAdminData] = useState<AdminUserFormData | null>(null)

  const { data: statusData, isLoading: statusLoading } = useSetupStatus()
  const { data: configResponse, isLoading: configLoading } = useSetupConfig({
    enabled: statusData?.setup_needed === true,
  })
  const completeSetup = useCompleteSetup()
  const envPreview = useSetupEnvPreview(configData, step === 'review' && configData !== null)

  const getInitialConfigValues = (): Partial<SetupConfigFormData> => {
    if (configResponse?.config) {
      return {
        jwt_secret: configResponse.config.jwt_secret || DEFAULT_SETUP_CONFIG.jwt_secret,
        refresh_secret: configResponse.config.refresh_secret || DEFAULT_SETUP_CONFIG.refresh_secret,
        addr: configResponse.config.addr || DEFAULT_SETUP_CONFIG.addr,
        gin_mode: (configResponse.config.gin_mode === 'debug' || configResponse.config.gin_mode === 'release')
          ? configResponse.config.gin_mode : DEFAULT_SETUP_CONFIG.gin_mode,
        debug: (configResponse.config.debug === 'true' || configResponse.config.debug === 'false')
          ? configResponse.config.debug : DEFAULT_SETUP_CONFIG.debug,
        db_type: configResponse.config.db_type || DEFAULT_SETUP_CONFIG.db_type,
        db_dsn: configResponse.config.db_dsn || DEFAULT_SETUP_CONFIG.db_dsn,
        prometheus_enabled: (configResponse.config.prometheus_enabled === 'true' || configResponse.config.prometheus_enabled === 'false')
          ? configResponse.config.prometheus_enabled : DEFAULT_SETUP_CONFIG.prometheus_enabled,
        prometheus_auth: (configResponse.config.prometheus_auth === 'true' || configResponse.config.prometheus_auth === 'false')
          ? configResponse.config.prometheus_auth : DEFAULT_SETUP_CONFIG.prometheus_auth,
        prometheus_token: configResponse.config.prometheus_token || DEFAULT_SETUP_CONFIG.prometheus_token,
      }
    }
    return {}
  }

  useEffect(() => {
    if (statusData && !statusData.setup_needed) navigate('/auth')
  }, [statusData, navigate])

  const handleCompleteSetup = async () => {
    if (!configData || !adminData) return
    try {
      await completeSetup.mutateAsync({
        config: configData,
        admin_email: adminData.email,
        admin_password: adminData.password,
      })
      setStep('success')
    } catch (error) {
      console.error('Setup completion error:', error)
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
            {step === 'welcome' && <WelcomeWidget onNext={() => setStep('config')} />}
            {step === 'config' && (
              <ConfigFormWidget
                initialValues={getInitialConfigValues()}
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
                envContent={envPreview.data}
                envLoading={envPreview.isLoading}
                envError={envPreview.error}
                onBack={() => setStep('admin')}
                onComplete={handleCompleteSetup}
                isCompleting={completeSetup.isPending}
                error={completeSetup.error}
              />
            )}
            {step === 'success' && <SuccessWidget />}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
