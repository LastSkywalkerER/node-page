import { useState } from 'react';
import { useForm } from 'react-hook-form';
import { yupResolver } from '@hookform/resolvers/yup';
import { AxiosError } from 'axios';
import { Button } from '../../shared/ui/button';
import { FormInputField, FormPasswordField } from '../../shared/ui/form-field';
import { Alert, AlertDescription } from '../../shared/ui/alert';
import { Loader2 } from 'lucide-react';
import { useRegister } from './useRegister';
import { registerSchema, RegisterFormData } from './schemas';

interface RegisterWidgetProps {
  onSwitchToLogin: () => void;
}

export function RegisterWidget({ onSwitchToLogin }: RegisterWidgetProps) {
  const [error, setError] = useState<string | null>(null);

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<RegisterFormData>({
    resolver: yupResolver(registerSchema),
  });

  const registerMutation = useRegister();

  const onSubmit = async (data: RegisterFormData) => {
    try {
      setError(null);
      await registerMutation.mutateAsync({
        email: data.email,
        password: data.password,
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

  return (
    <form onSubmit={handleSubmit(onSubmit)} className="space-y-4">
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
        disabled={isSubmitting}
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

      <div className="text-center">
        <button
          type="button"
          onClick={onSwitchToLogin}
          className="text-sm text-blue-400 hover:text-blue-300"
        >
          Already have an account? Sign in
        </button>
      </div>
    </form>
  );
}

