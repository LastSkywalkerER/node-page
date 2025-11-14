import React from 'react';
import { Button } from '../../shared/ui/button';
import { Alert, AlertDescription } from '../../shared/ui/alert';
import { SetupConfigFormData, AdminUserFormData } from './schemas';

export const REVIEW_STEP_META = {
  title: 'Review',
  description: 'Review your configuration before completing setup',
} as const;

interface ReviewWidgetProps {
  configData: SetupConfigFormData;
  adminData: AdminUserFormData;
  onBack: () => void;
  onComplete: () => void;
  isCompleting: boolean;
  error: Error | null;
}

export function ReviewWidget({
  configData,
  adminData,
  onBack,
  onComplete,
  isCompleting,
  error,
}: ReviewWidgetProps) {
  return (
    <div className="space-y-6">
      <div className="space-y-4">
        <h3 className="text-white font-semibold">Configuration</h3>
        <div className="bg-slate-900/50 p-4 rounded-md space-y-2 text-sm">
          <div className="flex justify-between">
            <span className="text-slate-400">Server Address:</span>
            <span className="text-white">{configData.addr}</span>
          </div>
          <div className="flex justify-between">
            <span className="text-slate-400">Gin Mode:</span>
            <span className="text-white">{configData.gin_mode}</span>
          </div>
          <div className="flex justify-between">
            <span className="text-slate-400">Debug:</span>
            <span className="text-white">{configData.debug}</span>
          </div>
          <div className="flex justify-between">
            <span className="text-slate-400">Database:</span>
            <span className="text-white">{configData.db_dsn}</span>
          </div>
          <div className="flex justify-between">
            <span className="text-slate-400">JWT Secret:</span>
            <span className="text-white">••••••••</span>
          </div>
          <div className="flex justify-between">
            <span className="text-slate-400">Refresh Secret:</span>
            <span className="text-white">••••••••</span>
          </div>
        </div>
      </div>

      <div className="space-y-4">
        <h3 className="text-white font-semibold">Admin Account</h3>
        <div className="bg-slate-900/50 p-4 rounded-md space-y-2 text-sm">
          <div className="flex justify-between">
            <span className="text-slate-400">Email:</span>
            <span className="text-white">{adminData.email}</span>
          </div>
        </div>
      </div>

      {error && (
        <Alert className="bg-red-900/50 border-red-700">
          <AlertDescription className="text-red-200">
            {error instanceof Error
              ? error.message
              : 'Failed to complete setup. Please try again.'}
          </AlertDescription>
        </Alert>
      )}

      <div className="flex gap-2">
        <Button
          type="button"
          variant="secondary"
          onClick={onBack}
          className="bg-slate-700 text-white hover:bg-slate-600 border-slate-600"
        >
          Back
        </Button>
        <Button
          onClick={onComplete}
          disabled={isCompleting}
          className="flex-1"
        >
          {isCompleting ? 'Completing...' : 'Complete Setup'}
        </Button>
      </div>
    </div>
  );
}

