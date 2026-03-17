import { useState } from 'react';
import { useForm } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { Button } from '@/components/ui/button';
import { FormInputField, FormPasswordField } from '@/components/ui/form-field';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Loader2 } from 'lucide-react';
import { useLogin } from './useLogin';
import { loginSchema, LoginFormData } from './schemas';

export function LoginWidget() {
  const [error, setError] = useState<string | null>(null);

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<LoginFormData>({
    resolver: zodResolver(loginSchema),
  });

  const loginMutation = useLogin();

  const onSubmit = async (data: LoginFormData) => {
    try {
      setError(null);
      await loginMutation.mutateAsync(data);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Login failed');
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
        name="email"
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
        name="password"
        inputProps={{
          placeholder: 'Enter your password',
          className: 'bg-slate-700 border-slate-600 text-white placeholder:text-slate-400',
        }}
        error={errors.password}
      />

      <Button
        type="submit"
        className="w-full bg-blue-600 hover:bg-blue-700"
        disabled={isSubmitting}
      >
        {isSubmitting ? (
          <>
            <Loader2 className="mr-2 h-4 w-4 animate-spin" />
            Signing in...
          </>
        ) : (
          'Sign In'
        )}
      </Button>
    </form>
  );
}
