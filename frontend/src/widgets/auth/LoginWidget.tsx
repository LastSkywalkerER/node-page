import { useState } from 'react';
import { useForm } from 'react-hook-form';
import { yupResolver } from '@hookform/resolvers/yup';
import { Button } from '../../shared/ui/button';
import { Input } from '../../shared/ui/input';
import { Label } from '../../shared/ui/label';
import { Alert, AlertDescription } from '../../shared/ui/alert';
import { Loader2, Eye, EyeOff } from 'lucide-react';
import { useLogin } from './useLogin';
import { loginSchema, LoginFormData } from './schemas';

interface LoginWidgetProps {
  onSwitchToRegister: () => void;
}

export function LoginWidget({ onSwitchToRegister }: LoginWidgetProps) {
  const [showPassword, setShowPassword] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<LoginFormData>({
    resolver: yupResolver(loginSchema),
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

      <div className="space-y-2">
        <Label htmlFor="email" className="text-white">Email</Label>
        <Input
          id="email"
          type="email"
          placeholder="Enter your email"
          className="bg-slate-700 border-slate-600 text-white placeholder:text-slate-400"
          {...register('email')}
        />
        {errors.email && (
          <p className="text-sm text-red-400">{errors.email.message}</p>
        )}
      </div>

      <div className="space-y-2">
        <Label htmlFor="password" className="text-white">Password</Label>
        <div className="relative">
          <Input
            id="password"
            type={showPassword ? 'text' : 'password'}
            placeholder="Enter your password"
            className="bg-slate-700 border-slate-600 text-white placeholder:text-slate-400 pr-10"
            {...register('password')}
          />
          <button
            type="button"
            onClick={() => setShowPassword(!showPassword)}
            className="absolute right-3 top-1/2 -translate-y-1/2 text-slate-400 hover:text-white"
          >
            {showPassword ? <EyeOff size={16} /> : <Eye size={16} />}
          </button>
        </div>
        {errors.password && (
          <p className="text-sm text-red-400">{errors.password.message}</p>
        )}
      </div>

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

      <div className="text-center">
        <button
          type="button"
          onClick={onSwitchToRegister}
          className="text-sm text-blue-400 hover:text-blue-300"
        >
          Don't have an account? Sign up
        </button>
      </div>
    </form>
  );
}

