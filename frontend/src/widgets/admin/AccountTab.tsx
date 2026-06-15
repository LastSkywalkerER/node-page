import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'
import { isAxiosError } from 'axios'
import { apiClient } from '@/shared/lib/api'
import { authService } from '@/shared/lib/auth'
import { useUserStore } from '@/shared/store/user'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { FormPasswordField } from '@/components/ui/form-field'

// Mirrors the backend rule (internal/auth/users/service.go validPassword) and
// the setup wizard's adminUserSchema: min 8 chars, at least one letter + digit.
const passwordRule = z
  .string()
  .min(8, 'At least 8 characters')
  .regex(/[a-zA-Z]/, 'Must contain a letter')
  .regex(/\d/, 'Must contain a digit')

const schema = z
  .object({
    current_password: z.string().min(1, 'Required'),
    new_password: passwordRule,
    confirm_password: z.string().min(1, 'Required'),
  })
  .refine((d) => d.new_password === d.confirm_password, {
    message: 'Passwords do not match',
    path: ['confirm_password'],
  })

type FormData = z.infer<typeof schema>

export function AccountTab() {
  const navigate = useNavigate()
  const user = useUserStore((s) => s.user)
  const clearAuth = useUserStore((s) => s.clearAuth)

  const {
    register,
    handleSubmit,
    reset,
    formState: { errors, isSubmitting },
  } = useForm<FormData>({ resolver: zodResolver(schema) })

  const onSubmit = async (data: FormData) => {
    try {
      await apiClient.post('/users/me/password', {
        current_password: data.current_password,
        new_password: data.new_password,
      })
      reset()
      // Changing the password revokes all sessions server-side — log out and
      // bounce to the login screen so the user re-authenticates.
      toast.success('Password changed. Please sign in again.')
      try {
        await authService.logout()
      } catch {
        // ignore — token may already be invalidated
      }
      clearAuth()
      navigate('/auth')
    } catch (err) {
      const msg = isAxiosError(err)
        ? err.response?.data?.error || err.message
        : 'Failed to change password'
      toast.error(msg)
    }
  }

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="font-display text-lg tracking-wide">Account</CardTitle>
        <CardDescription>
          {user?.email ? (
            <>
              Signed in as <span className="font-medium text-foreground">{user.email}</span>. Change
              your password below — this signs you out everywhere.
            </>
          ) : (
            'Change your password — this signs you out everywhere.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className="pt-2">
        <form onSubmit={handleSubmit(onSubmit)} className="max-w-md space-y-4">
          <FormPasswordField
            label="Current password"
            name="current_password"
            required
            register={register('current_password')}
            error={errors.current_password}
            inputProps={{ autoComplete: 'current-password' }}
          />
          <FormPasswordField
            label="New password"
            name="new_password"
            required
            register={register('new_password')}
            error={errors.new_password}
            inputProps={{ autoComplete: 'new-password' }}
          />
          <FormPasswordField
            label="Confirm new password"
            name="confirm_password"
            required
            register={register('confirm_password')}
            error={errors.confirm_password}
            inputProps={{ autoComplete: 'new-password' }}
          />
          <Button type="submit" disabled={isSubmitting}>
            {isSubmitting ? 'Saving…' : 'Change password'}
          </Button>
        </form>
      </CardContent>
    </Card>
  )
}
