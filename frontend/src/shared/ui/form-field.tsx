import React from 'react';
import { FieldError, UseFormRegisterReturn } from 'react-hook-form';
import { Label } from './label';
import { Input, InputProps } from './input';
import { PasswordInput, PasswordInputProps } from './password-input';
import { Select, SelectProps, SelectOption } from './select';
import { cn } from '../lib/utils';

export interface FormFieldProps {
  label: string;
  required?: boolean;
  error?: FieldError;
  className?: string;
  labelClassName?: string;
  errorClassName?: string;
  children: React.ReactNode;
  id?: string;
}

export function FormField({
  label,
  required = false,
  error,
  className,
  labelClassName,
  errorClassName,
  children,
  id,
}: FormFieldProps) {
  return (
    <div className={cn('space-y-2 w-full', className)}>
      <Label htmlFor={id} className={cn('text-white', labelClassName)}>
        {label}
        {required && <span className="text-red-400"> *</span>}
      </Label>
      {children}
      {error && (
        <p className={cn('text-sm text-red-400', errorClassName)}>
          {error.message}
        </p>
      )}
    </div>
  );
}

export interface FormInputFieldProps extends Omit<FormFieldProps, 'children' | 'id'> {
  inputProps?: Omit<InputProps, 'id'>;
  register: UseFormRegisterReturn;
  name: string;
}

export function FormInputField({
  label,
  required,
  error,
  inputProps,
  register,
  name,
  className,
  labelClassName,
  errorClassName,
}: FormInputFieldProps) {
  return (
    <FormField
      label={label}
      required={required}
      error={error}
      className={className}
      labelClassName={labelClassName}
      errorClassName={errorClassName}
      id={name}
    >
      <Input id={name} {...register} {...inputProps} />
    </FormField>
  );
}

export interface FormPasswordFieldProps extends Omit<FormFieldProps, 'children' | 'id'> {
  inputProps?: Omit<PasswordInputProps, 'id'>;
  register: UseFormRegisterReturn;
  name: string;
}

export function FormPasswordField({
  label,
  required,
  error,
  inputProps,
  register,
  name,
  className,
  labelClassName,
  errorClassName,
}: FormPasswordFieldProps) {
  return (
    <FormField
      label={label}
      required={required}
      error={error}
      className={className}
      labelClassName={labelClassName}
      errorClassName={errorClassName}
      id={name}
    >
      <PasswordInput id={name} {...register} {...inputProps} />
    </FormField>
  );
}

export interface FormSelectFieldProps extends Omit<FormFieldProps, 'children' | 'id'> {
  selectProps?: Omit<SelectProps, 'id' | 'options'>;
  options: SelectOption[];
  register: UseFormRegisterReturn;
  name: string;
}

export function FormSelectField({
  label,
  required,
  error,
  selectProps,
  options,
  register,
  name,
  className,
  labelClassName,
  errorClassName,
}: FormSelectFieldProps) {
  return (
    <FormField
      label={label}
      required={required}
      error={error}
      className={className}
      labelClassName={labelClassName}
      errorClassName={errorClassName}
      id={name}
    >
      <Select id={name} options={options} {...register} {...selectProps} />
    </FormField>
  );
}

