import React from 'react';
import { useForm } from 'react-hook-form';
import { yupResolver } from '@hookform/resolvers/yup';
import { Button } from '../../shared/ui/button';
import { Input } from '../../shared/ui/input';
import { PasswordInput } from '../../shared/ui/password-input';
import { Label } from '../../shared/ui/label';
import { adminUserSchema, AdminUserFormData } from './schemas';

export const ADMIN_STEP_META = {
  title: 'Admin Account',
  description: 'Create your administrator account',
} as const;

interface AdminFormWidgetProps {
  onSubmit: (data: AdminUserFormData) => void;
  onBack: () => void;
}

export function AdminFormWidget({ onSubmit, onBack }: AdminFormWidgetProps) {
  const form = useForm<AdminUserFormData>({
    resolver: yupResolver(adminUserSchema),
    defaultValues: {
      email: '',
      password: '',
      confirmPassword: '',
    },
  });

  return (
    <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
      <div className="space-y-2">
        <Label htmlFor="email" className="text-white">
          Email <span className="text-red-400">*</span>
        </Label>
        <Input
          id="email"
          type="email"
          {...form.register('email')}
        />
        {form.formState.errors.email && (
          <p className="text-sm text-red-400">
            {form.formState.errors.email.message}
          </p>
        )}
      </div>

      <div className="space-y-2">
        <Label htmlFor="password" className="text-white">
          Password <span className="text-red-400">*</span>
        </Label>
        <PasswordInput
          id="password"
          {...form.register('password')}
        />
        {form.formState.errors.password && (
          <p className="text-sm text-red-400">
            {form.formState.errors.password.message}
          </p>
        )}
      </div>

      <div className="space-y-2">
        <Label htmlFor="confirmPassword" className="text-white">
          Confirm Password <span className="text-red-400">*</span>
        </Label>
        <PasswordInput
          id="confirmPassword"
          {...form.register('confirmPassword')}
        />
        {form.formState.errors.confirmPassword && (
          <p className="text-sm text-red-400">
            {form.formState.errors.confirmPassword.message}
          </p>
        )}
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

