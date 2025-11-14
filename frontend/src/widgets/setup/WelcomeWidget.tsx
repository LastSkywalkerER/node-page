import React from 'react';
import { Button } from '../../shared/ui/button';

export const WELCOME_STEP_META = {
  title: 'Welcome',
  description: 'Let\'s configure your System Stats installation',
} as const;

interface WelcomeWidgetProps {
  onNext: () => void;
}

export function WelcomeWidget({ onNext }: WelcomeWidgetProps) {
  return (
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
      <Button onClick={onNext} className="w-full">
        Get Started
      </Button>
    </div>
  );
}

