import { useEffect, useRef } from 'react';
import { useForm, useWatch } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { Button } from '@/components/ui/button';
import { FormInputField, FormSelectField, FormField } from '@/components/ui/form-field';
import { setupConfigSchema, SetupConfigFormData } from './schemas';
import { DEFAULT_SETUP_CONFIG } from '../../shared/config/setup';
import { PasswordInput } from '@/shared/ui/password-input';

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
    resolver: zodResolver(setupConfigSchema),
    defaultValues: {
      ...DEFAULT_SETUP_CONFIG,
      ...initialValues,
    },
  });

  const dbType = useWatch({ control: form.control, name: 'db_type' });
  const prevDbType = useRef(dbType);

  useEffect(() => {
    if (prevDbType.current !== dbType) {
      prevDbType.current = dbType;
      form.setValue('db_dsn', dbType === 'sqlite' ? 'stats.db' : '', { shouldValidate: false });
    }
  }, [dbType, form]);

  const handleGenerateSecret = (field: 'jwt_secret' | 'refresh_secret') => {
    form.setValue(field, generateRandomSecret(32), { shouldValidate: true });
  };

  return (
    <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
      <FormField
        label="Token Signing Secret"
        required
        error={form.formState.errors.jwt_secret}
        id="jwt_secret"
      >
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
      </FormField>

      <FormField
        label="Refresh Token Secret"
        required
        error={form.formState.errors.refresh_secret}
        id="refresh_secret"
      >
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
      </FormField>

      <FormInputField
        label="Server Address"
        register={form.register('addr')}
        name="addr"
        error={form.formState.errors.addr}
      />

      <FormSelectField
        label="Gin Mode"
        register={form.register('gin_mode')}
        name="gin_mode"
        options={[
          { value: 'release', label: 'Release' },
          { value: 'debug', label: 'Debug' },
        ]}
        error={form.formState.errors.gin_mode}
      />

      <FormSelectField
        label="Debug Logging"
        register={form.register('debug')}
        name="debug"
        options={[
          { value: 'false', label: 'Off' },
          { value: 'true', label: 'On' },
        ]}
        error={form.formState.errors.debug}
      />

      <FormSelectField
        label="Database"
        register={form.register('db_type')}
        name="db_type"
        options={[
          { value: 'sqlite', label: 'SQLite (file)' },
          { value: 'postgres', label: 'PostgreSQL' },
        ]}
        error={form.formState.errors.db_type}
      />

      {dbType === 'sqlite' ? (
        <FormInputField
          label="Database File Path"
          register={form.register('db_dsn')}
          name="db_dsn"
          inputProps={{ placeholder: 'stats.db' }}
          error={form.formState.errors.db_dsn}
        />
      ) : (
        <FormInputField
          label="PostgreSQL Connection String"
          register={form.register('db_dsn')}
          name="db_dsn"
          inputProps={{ placeholder: 'postgres://stats:secret@localhost:5432/node_stats?sslmode=disable' }}
          error={form.formState.errors.db_dsn}
        />
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
