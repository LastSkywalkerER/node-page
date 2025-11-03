import { useState } from 'react';
import { useForm } from 'react-hook-form';
import { yupResolver } from '@hookform/resolvers/yup';
import { AxiosError } from 'axios';
import { Button } from '../../shared/ui/button';
import { Input } from '../../shared/ui/input';
import { Label } from '../../shared/ui/label';
import { Alert, AlertDescription } from '../../shared/ui/alert';
import { Loader2, Eye, EyeOff } from 'lucide-react';
import { useRegister } from './useRegister';
import { registerSchema, RegisterFormData } from './schemas';

interface RegisterWidgetProps {
  onSwitchToLogin: () => void;
}

export function RegisterWidget({ onSwitchToLogin }: RegisterWidgetProps) {
  const [showPassword, setShowPassword] = useState(false);
  const [showConfirmPassword, setShowConfirmPassword] = useState(false);
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

      <div className="space-y-2">
        <Label htmlFor="register-email" className="text-white">Email</Label>
        <Input
          id="register-email"
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
        <Label htmlFor="register-password" className="text-white">Password</Label>
        <div className="relative">
          <Input
            id="register-password"
            type={showPassword ? 'text' : 'password'}
            placeholder="Create a password"
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

      <div className="space-y-2">
        <Label htmlFor="confirm-password" className="text-white">Confirm Password</Label>
        <div className="relative">
          <Input
            id="confirm-password"
            type={showConfirmPassword ? 'text' : 'password'}
            placeholder="Confirm your password"
            className="bg-slate-700 border-slate-600 text-white placeholder:text-slate-400 pr-10"
            {...register('confirmPassword')}
          />
          <button
            type="button"
            onClick={() => setShowConfirmPassword(!showConfirmPassword)}
            className="absolute right-3 top-1/2 -translate-y-1/2 text-slate-400 hover:text-white"
          >
            {showConfirmPassword ? <EyeOff size={16} /> : <Eye size={16} />}
          </button>
        </div>
        {errors.confirmPassword && (
          <p className="text-sm text-red-400">{errors.confirmPassword.message}</p>
        )}
      </div>

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

