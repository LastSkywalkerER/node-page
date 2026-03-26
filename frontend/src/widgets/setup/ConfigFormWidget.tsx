import { useEffect, useRef, type ReactNode } from 'react'
import { useForm, useWatch, Controller } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { Button } from '@/components/ui/button';
import { Label } from '@/components/ui/label';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { FormInputField, FormSelectField, FormField } from '@/components/ui/form-field';
import { Switch } from '@/shared/ui/switch';
import { PasswordInput } from '@/shared/ui/password-input';
import { setupConfigSchema, SetupConfigFormData, type MachineHintsResponse } from './schemas';
import { DEFAULT_SETUP_CONFIG } from '../../shared/config/setup';

export const CONFIG_STEP_META = {
  title: 'Configuration',
  description: 'Configure application settings',
} as const;

interface ConfigFormWidgetProps {
  initialValues?: Partial<SetupConfigFormData>;
  /** When true (server detected a container), show optional Docker host-metrics preset. */
  runningInDocker?: boolean;
  /** Server-suggested hostname / IPv4 from GET /setup/status (prefills empty fields once). */
  machineHints?: MachineHintsResponse | null;
  onSubmit: (data: SetupConfigFormData) => void;
  onBack: () => void;
}

interface ToggleRowProps {
  label: string;
  description?: string;
  checked: boolean;
  onCheckedChange: (val: boolean) => void;
  id: string;
}

interface SectionDividerProps {
  label: string;
}

function ToggleRow({ label, description, checked, onCheckedChange, id }: ToggleRowProps) {
  return (
    <div className="flex items-center justify-between gap-4 py-0.5">
      <div className="flex flex-col gap-0.5">
        <Label htmlFor={id} className="cursor-pointer text-sm text-slate-200">
          {label}
        </Label>
        {description && (
          <span className="text-xs text-slate-400">{description}</span>
        )}
      </div>
      <Switch id={id} checked={checked} onCheckedChange={onCheckedChange} />
    </div>
  );
}

interface AccordionProps {
  open: boolean;
  children: ReactNode;
}

function Accordion({ open, children }: AccordionProps) {
  return (
    <div
      className="overflow-hidden transition-all duration-300 ease-in-out"
      style={{
        maxHeight: open ? '800px' : '0px',
        opacity: open ? 1 : 0,
      }}
    >
      <div className="pt-3 space-y-4">
        {children}
      </div>
    </div>
  );
}

function SectionDivider({ label }: SectionDividerProps) {
  return (
    <div className="flex items-center gap-3 pt-1">
      <span className="text-xs font-medium uppercase tracking-wider text-slate-500">{label}</span>
      <div className="flex-1 border-t border-slate-700" />
    </div>
  );
}

export function ConfigFormWidget({ initialValues, runningInDocker, machineHints, onSubmit, onBack }: ConfigFormWidgetProps) {
  const form = useForm<SetupConfigFormData>({
    resolver: zodResolver(setupConfigSchema),
    defaultValues: {
      ...DEFAULT_SETUP_CONFIG,
      ...initialValues,
    },
  });

  const hintsApplied = useRef(false);
  useEffect(() => {
    if (hintsApplied.current || !machineHints) return;
    const h = form.getValues('node_stats_hostname');
    const ip = form.getValues('node_stats_ipv4');
    if (!h.trim() && machineHints.suggested_hostname) {
      form.setValue('node_stats_hostname', machineHints.suggested_hostname, { shouldValidate: true });
    }
    if (!ip.trim() && machineHints.suggested_ipv4) {
      form.setValue('node_stats_ipv4', machineHints.suggested_ipv4, { shouldValidate: true });
    }
    hintsApplied.current = true;
  }, [machineHints, form]);

  const dbType = useWatch({ control: form.control, name: 'db_type' });
  const prometheusEnabled = useWatch({ control: form.control, name: 'prometheus_enabled' });
  const prometheusAuth = useWatch({ control: form.control, name: 'prometheus_auth' });
  const prometheusToken = useWatch({ control: form.control, name: 'prometheus_token' });

  const prevDbType = useRef(dbType);
  useEffect(() => {
    if (prevDbType.current !== dbType) {
      prevDbType.current = dbType;
      form.setValue('db_dsn', dbType === 'sqlite' ? 'stats.db' : '', { shouldValidate: false });
    }
  }, [dbType, form]);

  const generateSecret = (length = 32) => {
    const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*';
    const array = new Uint8Array(length);
    crypto.getRandomValues(array);
    return Array.from(array, (b) => chars[b % chars.length]).join('');
  };

  const isPrometheusOn = prometheusEnabled === 'true';
  const isPrometheusAuthOn = prometheusAuth === 'true';

  return (
    <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-5">

      {/* === Secrets === */}
      <SectionDivider label="Secrets" />

      <FormField
        label="Token Signing Secret"
        required
        error={form.formState.errors.jwt_secret}
        id="jwt_secret"
      >
        <div className="flex gap-2">
          <PasswordInput id="jwt_secret" {...form.register('jwt_secret')} className="flex-1" />
          <Button
            type="button"
            variant="secondary"
            onClick={() => form.setValue('jwt_secret', generateSecret(), { shouldValidate: true })}
            className="bg-slate-700 text-white hover:bg-slate-600 border-slate-600 shrink-0"
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
        <div className="flex gap-2">
          <PasswordInput id="refresh_secret" {...form.register('refresh_secret')} className="flex-1" />
          <Button
            type="button"
            variant="secondary"
            onClick={() => form.setValue('refresh_secret', generateSecret(), { shouldValidate: true })}
            className="bg-slate-700 text-white hover:bg-slate-600 border-slate-600 shrink-0"
          >
            Generate
          </Button>
        </div>
      </FormField>

      {/* === Server === */}
      <SectionDivider label="Server" />

      <FormInputField
        label="Address"
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

      <Controller
        control={form.control}
        name="debug"
        render={({ field }) => (
          <ToggleRow
            id="debug"
            label="Debug Logging"
            description="Verbose logs — keep off in production"
            checked={field.value === 'true'}
            onCheckedChange={(val) => field.onChange(val ? 'true' : 'false')}
          />
        )}
      />

      <SectionDivider label="Machine identity" />
      <p className="text-xs text-slate-400 leading-relaxed">
        Optional labels for the local collector host. Leave hostname empty to use auto-detected name on the machine card and in breadcrumbs; set it to override (e.g. friendly name in Docker).
        Leave IPv4 empty for automatic detection. Non-empty values are written as{' '}
        <code className="rounded bg-black/30 px-1 font-mono text-[0.65rem]">NODE_STATS_HOSTNAME</code> and{' '}
        <code className="rounded bg-black/30 px-1 font-mono text-[0.65rem]">NODE_STATS_IPV4</code> in <code className="font-mono text-[0.65rem]">.env</code>.
      </p>
      <FormInputField
        label="Display hostname (optional)"
        register={form.register('node_stats_hostname')}
        name="node_stats_hostname"
        inputProps={{ placeholder: 'e.g. my-server', autoComplete: 'off' }}
        error={form.formState.errors.node_stats_hostname}
      />
      <FormInputField
        label="IPv4 override (optional)"
        register={form.register('node_stats_ipv4')}
        name="node_stats_ipv4"
        inputProps={{ placeholder: 'e.g. 192.168.1.10', autoComplete: 'off' }}
        error={form.formState.errors.node_stats_ipv4}
      />

      {runningInDocker && (
        <>
          <SectionDivider label="Docker" />
          <Alert className="border-amber-800/60 bg-amber-950/40 text-amber-100">
            <AlertDescription className="text-xs leading-relaxed text-amber-100/95 space-y-2">
              <p>
                This setup wizard is running inside a container. Enable the option below to add{' '}
                <code className="rounded bg-black/30 px-1 py-0.5 font-mono text-[0.7rem]">HOST_PROC</code>,{' '}
                <code className="rounded bg-black/30 px-1 py-0.5 font-mono text-[0.7rem]">HOST_SYS</code>,{' '}
                <code className="rounded bg-black/30 px-1 py-0.5 font-mono text-[0.7rem]">HOST_ETC</code>,{' '}
                <code className="rounded bg-black/30 px-1 py-0.5 font-mono text-[0.7rem]">HOST_ROOT=/host</code>,{' '}
                <code className="rounded bg-black/30 px-1 py-0.5 font-mono text-[0.7rem]">NODE_HOST_ALIAS</code>, and a
                typical SQLite path under <code className="font-mono text-[0.7rem]">/app/data</code> to the generated{' '}
                <code className="font-mono text-[0.7rem]">.env</code>.
              </p>
              <p className="font-medium text-amber-50/95">Match your <code className="font-mono text-[0.7rem]">docker-compose.yml</code> to that:</p>
              <ul className="list-disc pl-4 space-y-1 text-amber-100/90">
                <li>
                  <span className="font-medium">Volumes:</span> bind-mount host root read-only, e.g.{' '}
                  <code className="rounded bg-black/30 px-1 py-0.5 font-mono text-[0.65rem]">/:/host:ro</code>; mount{' '}
                  <code className="font-mono text-[0.65rem]">/var/run/docker.sock</code> for Docker metrics.
                </li>
                <li>
                  <span className="font-medium">Environment:</span> the preset aligns with{' '}
                  <code className="rounded bg-black/30 px-1 py-0.5 font-mono text-[0.65rem]">HOST_ROOT=/host</code> (disk totals use the host bind-mount, not the container overlay).
                </li>
                <li>
                  <span className="font-medium">Linux:</span> add{' '}
                  <code className="rounded bg-black/30 px-1 py-0.5 font-mono text-[0.65rem]">extra_hosts: [&quot;host.docker.internal:host-gateway&quot;]</code>{' '}
                  if the app must reach the host by name.
                </li>
              </ul>
            </AlertDescription>
          </Alert>
          <Controller
            control={form.control}
            name="docker_host_metrics_compat"
            render={({ field }) => (
              <ToggleRow
                id="docker_host_metrics_compat"
                label="Docker host metrics compatibility"
                description="Append HOST_PROC, HOST_SYS, HOST_ETC, HOST_ROOT, NODE_HOST_ALIAS; use /app/data/stats.db for SQLite when DSN is still the default file name"
                checked={field.value}
                onCheckedChange={(enabled) => {
                  field.onChange(enabled);
                  const dsn = form.getValues('db_dsn');
                  const dtype = form.getValues('db_type');
                  if (enabled) {
                    if (dtype === 'sqlite' && (dsn === '' || dsn === 'stats.db')) {
                      form.setValue('db_dsn', '/app/data/stats.db', { shouldValidate: true });
                    }
                  } else if (dsn === '/app/data/stats.db') {
                    form.setValue('db_dsn', 'stats.db', { shouldValidate: true });
                  }
                }}
              />
            )}
          />
        </>
      )}

      {/* === Database === */}
      <SectionDivider label="Database" />

      <FormSelectField
        label="Type"
        register={form.register('db_type')}
        name="db_type"
        options={[
          { value: 'sqlite', label: 'SQLite (file)' },
          { value: 'postgres', label: 'PostgreSQL' },
        ]}
        error={form.formState.errors.db_type}
      />

      <Accordion open={dbType === 'sqlite'}>
        <FormInputField
          label="Database File Path"
          register={form.register('db_dsn')}
          name="db_dsn"
          inputProps={{ placeholder: 'stats.db' }}
          error={form.formState.errors.db_dsn}
        />
      </Accordion>

      <Accordion open={dbType === 'postgres'}>
        <FormInputField
          label="PostgreSQL Connection String"
          register={form.register('db_dsn')}
          name="db_dsn"
          inputProps={{ placeholder: 'postgres://stats:secret@localhost:5432/node_stats?sslmode=disable' }}
          error={form.formState.errors.db_dsn}
        />
      </Accordion>

      {/* === Prometheus === */}
      <SectionDivider label="Prometheus" />

      <Controller
        control={form.control}
        name="prometheus_enabled"
        render={({ field }) => (
          <ToggleRow
            id="prometheus_enabled"
            label="Enable Prometheus Metrics"
            description="Exposes /api/v1/metrics for scraping"
            checked={field.value === 'true'}
            onCheckedChange={(val) => {
              field.onChange(val ? 'true' : 'false');
              if (!val) {
                form.setValue('prometheus_auth', 'false');
                form.setValue('prometheus_token', '');
              }
            }}
          />
        )}
      />

      <Accordion open={isPrometheusOn}>
        <Controller
          control={form.control}
          name="prometheus_auth"
          render={({ field }) => (
            <ToggleRow
              id="prometheus_auth"
              label="Require Bearer Token"
              description="Protect the /metrics endpoint with a static token"
              checked={field.value === 'true'}
              onCheckedChange={(val) => {
                field.onChange(val ? 'true' : 'false');
                if (!val) form.setValue('prometheus_token', '');
              }}
            />
          )}
        />

        <Accordion open={isPrometheusAuthOn}>
          <FormField
            label="Bearer Token"
            error={form.formState.errors.prometheus_token}
            id="prometheus_token"
          >
            <div className="flex gap-2">
              <PasswordInput
                id="prometheus_token"
                {...form.register('prometheus_token')}
                className="flex-1"
                placeholder="Paste or generate a secure token"
              />
              <Button
                type="button"
                variant="secondary"
                onClick={() => form.setValue('prometheus_token', generateSecret(), { shouldValidate: true })}
                className="bg-slate-700 text-white hover:bg-slate-600 border-slate-600 shrink-0"
                disabled={!!prometheusToken && prometheusToken.length > 0 && false}
              >
                Generate
              </Button>
            </div>
          </FormField>
        </Accordion>
      </Accordion>

      {/* === Actions === */}
      <div className="flex gap-2 pt-2">
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
