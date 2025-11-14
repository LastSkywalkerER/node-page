import React from 'react';
import { useForm } from 'react-hook-form';
import { yupResolver } from '@hookform/resolvers/yup';
import { Button } from '../../shared/ui/button';
import { FormInputField, FormPasswordField } from '../../shared/ui/form-field';
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
      <FormInputField
        label="Email"
        required
        register={form.register('email')}
        name="email"
        inputProps={{ type: 'email' }}
        error={form.formState.errors.email}
      />

      <FormPasswordField
        label="Password"
        required
        register={form.register('password')}
        name="password"
        error={form.formState.errors.password}
      />

      <FormPasswordField
        label="Confirm Password"
        required
        register={form.register('confirmPassword')}
        name="confirmPassword"
        error={form.formState.errors.confirmPassword}
      />

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

