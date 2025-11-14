import React from 'react';
import { useForm } from 'react-hook-form';
import { yupResolver } from '@hookform/resolvers/yup';
import { Button } from '../../shared/ui/button';
import { Input } from '../../shared/ui/input';
import { PasswordInput } from '../../shared/ui/password-input';
import { Label } from '../../shared/ui/label';
import { setupConfigSchema, SetupConfigFormData } from './schemas';
import { DEFAULT_SETUP_CONFIG } from '../../shared/config/setup';

export const CONFIG_STEP_META = {
  title: 'Configuration',
  description: 'Configure application settings',
} as const;

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
      <div className="space-y-2 w-full">
        <Label htmlFor="jwt_secret" className="text-white">
          JWT Secret <span className="text-red-400">*</span>
        </Label>
        <div className="flex gap-2 w-full">
          <PasswordInput
            id="jwt_secret"
            {...form.register('jwt_secret')}
            className="flex-1"
          />
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

      <div className="space-y-2 w-full">
        <Label htmlFor="refresh_secret" className="text-white">
          Refresh Secret <span className="text-red-400">*</span>
        </Label>
        <div className="flex gap-2 w-full">
          <PasswordInput
            id="refresh_secret"
            {...form.register('refresh_secret')}
            className="flex-1"
          />
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

