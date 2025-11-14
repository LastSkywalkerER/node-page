import React from 'react';
import { useNavigate } from 'react-router-dom';
import { Button } from '../../shared/ui/button';
import { Alert, AlertDescription } from '../../shared/ui/alert';

export const SUCCESS_STEP_META = {
  title: 'Setup Complete',
  description: 'Setup completed successfully!',
} as const;

export function SuccessWidget() {
  const navigate = useNavigate();

  return (
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
  );
}

