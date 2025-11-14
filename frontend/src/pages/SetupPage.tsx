import { useState, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '../shared/ui/card';
import { Button } from '../shared/ui/button';
import { Alert, AlertDescription } from '../shared/ui/alert';
import { useSetupStatus, useSetupConfig, useCompleteSetup } from '../widgets/setup/useSetup';
import { SetupConfigFormData, AdminUserFormData } from '../widgets/setup/schemas';
import { ConfigFormWidget } from '../widgets/setup/ConfigFormWidget';
import { AdminFormWidget } from '../widgets/setup/AdminFormWidget';
import { ReviewWidget } from '../widgets/setup/ReviewWidget';
import { DEFAULT_SETUP_CONFIG } from '../shared/config/setup';

type Step = 'welcome' | 'config' | 'admin' | 'review' | 'success';

export function SetupPage() {
  const navigate = useNavigate();
  const [step, setStep] = useState<Step>('welcome');
  const [configData, setConfigData] = useState<SetupConfigFormData | null>(null);
  const [adminData, setAdminData] = useState<AdminUserFormData | null>(null);

  const { data: statusData, isLoading: statusLoading } = useSetupStatus();
  const { data: configResponse, isLoading: configLoading } = useSetupConfig({
    enabled: statusData?.setup_needed === true,
  });
  const completeSetup = useCompleteSetup();

  // Prepare initial values for config form
  const getInitialConfigValues = (): Partial<SetupConfigFormData> => {
    if (configResponse?.config) {
      return {
        jwt_secret: configResponse.config.jwt_secret || DEFAULT_SETUP_CONFIG.jwt_secret,
        refresh_secret: configResponse.config.refresh_secret || DEFAULT_SETUP_CONFIG.refresh_secret,
        addr: configResponse.config.addr || DEFAULT_SETUP_CONFIG.addr,
        gin_mode: (configResponse.config.gin_mode === 'debug' || configResponse.config.gin_mode === 'release')
          ? configResponse.config.gin_mode
          : DEFAULT_SETUP_CONFIG.gin_mode,
        debug: (configResponse.config.debug === 'true' || configResponse.config.debug === 'false')
          ? configResponse.config.debug
          : DEFAULT_SETUP_CONFIG.debug,
        db_type: configResponse.config.db_type || DEFAULT_SETUP_CONFIG.db_type,
        db_dsn: configResponse.config.db_dsn || DEFAULT_SETUP_CONFIG.db_dsn,
      };
    }
    return {};
  };

  // Check if setup is already completed
  useEffect(() => {
    if (statusData && !statusData.setup_needed) {
      navigate('/auth');
    }
  }, [statusData, navigate]);

  const handleConfigSubmit = (data: SetupConfigFormData) => {
    setConfigData(data);
    setStep('admin');
  };

  const handleAdminSubmit = (data: AdminUserFormData) => {
    setAdminData(data);
    setStep('review');
  };

  const handleCompleteSetup = async () => {
    if (!configData || !adminData) return;

    try {
      await completeSetup.mutateAsync({
        config: configData,
        admin_email: adminData.email,
        admin_password: adminData.password,
      });
      setStep('success');
    } catch (error) {
      console.error('Setup completion error:', error);
    }
  };

  if (statusLoading || configLoading) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-slate-900 via-purple-900 to-slate-900">
        <Card className="w-full max-w-md bg-slate-800/50 border-slate-700">
          <CardContent className="pt-6">
            <div className="text-center text-white">Loading...</div>
          </CardContent>
        </Card>
      </div>
    );
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-slate-900 via-purple-900 to-slate-900 p-4">
      <div className="w-full max-w-2xl">
        <div className="text-center mb-8">
          <h1 className="text-4xl font-bold text-white mb-2">System Stats</h1>
          <p className="text-slate-400">Initial Setup Wizard</p>
        </div>

        <Card className="bg-slate-800/50 border-slate-700">
          <CardHeader>
            <CardTitle className="text-white">
              {step === 'welcome' && 'Welcome'}
              {step === 'config' && 'Configuration'}
              {step === 'admin' && 'Admin Account'}
              {step === 'review' && 'Review'}
              {step === 'success' && 'Setup Complete'}
            </CardTitle>
            <CardDescription className="text-slate-400">
              {step === 'welcome' && 'Let\'s configure your System Stats installation'}
              {step === 'config' && 'Configure application settings'}
              {step === 'admin' && 'Create your administrator account'}
              {step === 'review' && 'Review your configuration before completing setup'}
              {step === 'success' && 'Setup completed successfully!'}
            </CardDescription>
          </CardHeader>
          <CardContent>
            {/* Welcome Step */}
            {step === 'welcome' && (
              <div className="space-y-6">
                <p className="text-slate-300">
                  Welcome to System Stats! This wizard will help you configure your installation.
                </p>
                <div className="space-y-2">
                  <h3 className="text-white font-semibold">You'll need to configure:</h3>
                  <ul className="list-disc list-inside text-slate-300 space-y-1">
                    <li>Application configuration (server address, database, etc.)</li>
                    <li>Security secrets (JWT tokens)</li>
                    <li>Administrator account</li>
                  </ul>
                </div>
                <Button onClick={() => setStep('config')} className="w-full">
                  Get Started
                </Button>
              </div>
            )}

            {/* Configuration Step */}
            {step === 'config' && (
              <ConfigFormWidget
                initialValues={getInitialConfigValues()}
                onSubmit={handleConfigSubmit}
                onBack={() => setStep('welcome')}
              />
            )}

            {/* Admin User Step */}
            {step === 'admin' && (
              <AdminFormWidget
                onSubmit={handleAdminSubmit}
                onBack={() => setStep('config')}
              />
            )}

            {/* Review Step */}
            {step === 'review' && configData && adminData && (
              <ReviewWidget
                configData={configData}
                adminData={adminData}
                onBack={() => setStep('admin')}
                onComplete={handleCompleteSetup}
                isCompleting={completeSetup.isPending}
                error={completeSetup.error}
              />
            )}

            {/* Success Step */}
            {step === 'success' && (
              <div className="space-y-6">
                <Alert className="bg-green-900/50 border-green-700">
                  <AlertDescription className="text-green-200">
                    Setup completed successfully! Please restart the server for changes to take effect.
                  </AlertDescription>
                </Alert>
                <Button onClick={() => navigate('/auth')} className="w-full">
                  Go to Login
                </Button>
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

