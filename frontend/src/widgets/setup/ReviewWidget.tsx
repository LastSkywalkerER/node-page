import React, { useState } from 'react';
import { Button } from '@/components/ui/button';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { AdminUserFormData } from './schemas';
import { Copy, Check, Loader2 } from 'lucide-react';
import { toast } from 'sonner';

export const REVIEW_STEP_META = {
  title: 'Review',
  description: 'Confirm your settings and copy the generated .env if you deploy with volumes',
} as const;

interface ReviewWidgetProps {
  adminData: AdminUserFormData;
  envContent: string | undefined;
  envLoading: boolean;
  envError: Error | null;
  onBack: () => void;
  onComplete: () => void;
  isCompleting: boolean;
  error: Error | null;
}

export function ReviewWidget({
  adminData,
  envContent,
  envLoading,
  envError,
  onBack,
  onComplete,
  isCompleting,
  error,
}: ReviewWidgetProps) {
  const [copied, setCopied] = useState(false);

  const handleCopyEnv = async () => {
    if (!envContent) return;
    try {
      await navigator.clipboard.writeText(envContent);
      setCopied(true);
      toast.success('.env copied to clipboard');
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      toast.error('Copy failed — select the text and copy manually');
    }
  };

  return (
    <div className="space-y-6">
      <div className="space-y-2">
        <h3 className="text-white font-semibold">Admin account</h3>
        <div className="bg-slate-900/50 p-4 rounded-md text-sm">
          <div className="flex justify-between gap-4">
            <span className="text-slate-400 shrink-0">Email</span>
            <span className="text-white text-right break-all">{adminData.email}</span>
          </div>
        </div>
      </div>

      <div className="space-y-2">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h3 className="text-white font-semibold">Generated <code className="text-slate-300">.env</code></h3>
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={!envContent || envLoading}
            onClick={handleCopyEnv}
            className="h-8 border-slate-600 bg-slate-800/80 text-slate-100 hover:bg-slate-700"
          >
            {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
            <span className="ml-1.5">{copied ? 'Copied' : 'Copy'}</span>
          </Button>
        </div>
        <p className="text-muted-foreground text-xs leading-relaxed">
          This is exactly what the server will write to <code className="text-slate-400">.env</code> (e.g.{' '}
          <code className="text-slate-400">/app/.env</code> in Docker). If something still fails after you finish
          setup, paste this file manually on the host or into your volume, then restart the container.
        </p>
        {envLoading && (
          <div className="flex items-center gap-2 rounded-md border border-slate-700 bg-slate-900/50 px-4 py-8 text-sm text-slate-400 justify-center">
            <Loader2 className="h-4 w-4 animate-spin" />
            Generating preview…
          </div>
        )}
        {envError && !envLoading && (
          <Alert className="bg-red-900/50 border-red-700">
            <AlertDescription className="text-red-200">
              {envError.message || 'Could not load .env preview.'}
            </AlertDescription>
          </Alert>
        )}
        {envContent && !envLoading && (
          <pre className="max-h-64 overflow-auto rounded-md border border-slate-700 bg-slate-950 p-3 text-left text-xs leading-relaxed text-slate-200 font-mono whitespace-pre-wrap break-all">
            {envContent}
          </pre>
        )}
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
        <Button onClick={onComplete} disabled={isCompleting} className="flex-1">
          {isCompleting ? 'Completing...' : 'Complete Setup'}
        </Button>
      </div>
    </div>
  );
}
