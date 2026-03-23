import { useState, useEffect } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { useQuery } from '@tanstack/react-query';
import { AxiosError } from 'axios';
import { apiClient } from '@/shared/lib/api';
import { Button } from '@/components/ui/button';
import { FormInputField, FormPasswordField } from '@/components/ui/form-field';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Loader2 } from 'lucide-react';
import { useRegister } from './useRegister';
import { registerSchema, RegisterFormData } from './schemas';

interface RegisterWidgetProps {
  inviteToken?: string;
  onSwitchToLogin: () => void;
}

export function RegisterWidget({ inviteToken, onSwitchToLogin }: RegisterWidgetProps) {
  const [error, setError] = useState<string | null>(null);

  const {
    data: inviteData,
    error: inviteError,
    isLoading: inviteLoading,
  } = useQuery({
    queryKey: ['invite-validate', inviteToken],
    queryFn: async () => {
      const res = await apiClient.get<{ data: { email: string } }>(
        `/invitations/validate?token=${encodeURIComponent(inviteToken!)}`,
      );
      return res.data.data;
    },
    enabled: !!inviteToken,
    retry: false,
  });

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
    setValue,
  } = useForm<RegisterFormData>({
    resolver: zodResolver(registerSchema),
    defaultValues: { email: '' },
  });

  useEffect(() => {
    if (inviteData?.email) {
      setValue('email', inviteData.email);
    }
  }, [inviteData?.email, setValue]);

  const registerMutation = useRegister();

  const onSubmit = async (data: RegisterFormData) => {
    try {
      setError(null);
      await registerMutation.mutateAsync({
        email: data.email,
        password: data.password,
        inviteToken: inviteToken ?? undefined,
      });
    } catch (err) {
      let errorMessage = 'Registration failed';
      if (err instanceof AxiosError && err.response?.data) {
        const errorData = err.response.data as { error?: string; message?: string };
        errorMessage = errorData.error || errorData.message || errorMessage;
      } else if (err instanceof Error) {
        errorMessage = err.message;
      }
      setError(errorMessage);
    }
  };

  const inviteInvalid = !!inviteToken && !!inviteError;

  return (
    <form onSubmit={handleSubmit(onSubmit)} className="space-y-4">
      {inviteInvalid && (
        <Alert variant="destructive">
          <AlertDescription>
            Invalid or already used invitation link. Please request a new one.
          </AlertDescription>
        </Alert>
      )}
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      <FormInputField
        label="Email"
        register={register('email')}
        name="register-email"
        inputProps={{
          type: 'email',
          placeholder: 'Enter your email',
          className: 'bg-slate-700 border-slate-600 text-white placeholder:text-slate-400',
          readOnly: !!inviteToken,
        }}
        error={errors.email}
      />

      <FormPasswordField
        label="Password"
        register={register('password')}
        name="register-password"
        inputProps={{
          placeholder: 'Create a password',
          className: 'bg-slate-700 border-slate-600 text-white placeholder:text-slate-400',
        }}
        error={errors.password}
      />

      <FormPasswordField
        label="Confirm Password"
        register={register('confirmPassword')}
        name="confirm-password"
        inputProps={{
          placeholder: 'Confirm your password',
          className: 'bg-slate-700 border-slate-600 text-white placeholder:text-slate-400',
        }}
        error={errors.confirmPassword}
      />

      <Button
        type="submit"
        className="w-full bg-green-600 hover:bg-green-700"
        disabled={isSubmitting || (!!inviteToken && !inviteData)}
      >
        {isSubmitting ? (
          <>
            <Loader2 className="mr-2 h-4 w-4 animate-spin" />
            Creating account...
          </>
        ) : (
          'Create Account'
        )}
      </Button>

      {onSwitchToLogin && (
        <div className="text-center">
          <button
            type="button"
            onClick={onSwitchToLogin}
            className="text-sm text-blue-400 hover:text-blue-300"
          >
            Already have an account? Sign in
          </button>
        </div>
      )}
    </form>
  );
}

