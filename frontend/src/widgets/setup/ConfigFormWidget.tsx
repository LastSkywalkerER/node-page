import React, { useState } from 'react';
import { useForm } from 'react-hook-form';
import { yupResolver } from '@hookform/resolvers/yup';
import { Eye, EyeOff } from 'lucide-react';
import { Button } from '../../shared/ui/button';
import { Input } from '../../shared/ui/input';
import { Label } from '../../shared/ui/label';
import { setupConfigSchema, SetupConfigFormData } from './schemas';
import { DEFAULT_SETUP_CONFIG } from '../../shared/config/setup';

interface ConfigFormWidgetProps {
  initialValues?: Partial<SetupConfigFormData>;
  onSubmit: (data: SetupConfigFormData) => void;
  onBack: () => void;
}

export function ConfigFormWidget({
  initialValues,
  onSubmit,
  onBack,
}: ConfigFormWidgetProps) {
  const [showJwtSecret, setShowJwtSecret] = useState(false);
  const [showRefreshSecret, setShowRefreshSecret] = useState(false);

  const form = useForm<SetupConfigFormData>({
    resolver: yupResolver(setupConfigSchema),
    defaultValues: {
      ...DEFAULT_SETUP_CONFIG,
      ...initialValues,
    },
  });

  const handleGenerateSecret = (field: 'jwt_secret' | 'refresh_secret') => {
    const secret = generateRandomSecret(32);
    form.setValue(field, secret);
  };

  return (
    <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
      <div className="space-y-2">
        <Label htmlFor="jwt_secret" className="text-white">
          JWT Secret <span className="text-red-400">*</span>
        </Label>
        <div className="flex gap-2">
          <div className="relative flex-1">
            <Input
              id="jwt_secret"
              type={showJwtSecret ? 'text' : 'password'}
              {...form.register('jwt_secret')}
              className="pr-10"
            />
            <button
              type="button"
              onClick={() => setShowJwtSecret(!showJwtSecret)}
              className="absolute right-3 top-1/2 -translate-y-1/2 text-slate-400 hover:text-slate-200 transition-colors"
            >
              {showJwtSecret ? (
                <EyeOff className="h-4 w-4" />
              ) : (
                <Eye className="h-4 w-4" />
              )}
            </button>
          </div>
          <Button
            type="button"
            variant="secondary"
            onClick={() => handleGenerateSecret('jwt_secret')}
            className="bg-slate-700 text-white hover:bg-slate-600 border-slate-600"
          >
            Generate
          </Button>
        </div>
        {form.formState.errors.jwt_secret && (
          <p className="text-sm text-red-400">
            {form.formState.errors.jwt_secret.message}
          </p>
        )}
      </div>

      <div className="space-y-2">
        <Label htmlFor="refresh_secret" className="text-white">
          Refresh Secret <span className="text-red-400">*</span>
        </Label>
        <div className="flex gap-2">
          <div className="relative flex-1">
            <Input
              id="refresh_secret"
              type={showRefreshSecret ? 'text' : 'password'}
              {...form.register('refresh_secret')}
              className="pr-10"
            />
            <button
              type="button"
              onClick={() => setShowRefreshSecret(!showRefreshSecret)}
              className="absolute right-3 top-1/2 -translate-y-1/2 text-slate-400 hover:text-slate-200 transition-colors"
            >
              {showRefreshSecret ? (
                <EyeOff className="h-4 w-4" />
              ) : (
                <Eye className="h-4 w-4" />
              )}
            </button>
          </div>
          <Button
            type="button"
            variant="secondary"
            onClick={() => handleGenerateSecret('refresh_secret')}
            className="bg-slate-700 text-white hover:bg-slate-600 border-slate-600"
          >
            Generate
          </Button>
        </div>
        {form.formState.errors.refresh_secret && (
          <p className="text-sm text-red-400">
            {form.formState.errors.refresh_secret.message}
          </p>
        )}
      </div>

      <div className="space-y-2">
        <Label htmlFor="addr" className="text-white">Server Address</Label>
        <Input id="addr" {...form.register('addr')} />
        {form.formState.errors.addr && (
          <p className="text-sm text-red-400">
            {form.formState.errors.addr.message}
          </p>
        )}
      </div>

      <div className="space-y-2">
        <Label htmlFor="gin_mode" className="text-white">Gin Mode</Label>
        <select
          id="gin_mode"
          {...form.register('gin_mode')}
          className="flex h-10 w-full rounded-md border border-slate-600 bg-slate-800 px-3 py-2 text-sm text-white"
        >
          <option value="release">Release</option>
          <option value="debug">Debug</option>
        </select>
      </div>

      <div className="space-y-2">
        <Label htmlFor="debug" className="text-white">Debug Mode</Label>
        <select
          id="debug"
          {...form.register('debug')}
          className="flex h-10 w-full rounded-md border border-slate-600 bg-slate-800 px-3 py-2 text-sm text-white"
        >
          <option value="false">False</option>
          <option value="true">True</option>
        </select>
      </div>

      <div className="space-y-2">
        <Label htmlFor="db_dsn" className="text-white">Database File Path</Label>
        <Input id="db_dsn" {...form.register('db_dsn')} />
      </div>

      <div className="flex gap-2">
        <Button
          type="button"
          variant="secondary"
          onClick={onBack}
          className="bg-slate-700 text-white hover:bg-slate-600 border-slate-600"
        >
          Back
        </Button>
        <Button type="submit" className="flex-1">
          Continue
        </Button>
      </div>
    </form>
  );
}

function generateRandomSecret(length: number = 32): string {
  const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*';
  let result = '';
  const array = new Uint8Array(length);
  crypto.getRandomValues(array);
  for (let i = 0; i < length; i++) {
    result += chars[array[i] % chars.length];
  }
  return result;
}

