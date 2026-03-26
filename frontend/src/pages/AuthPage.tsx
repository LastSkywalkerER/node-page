import { useSearchParams, useNavigate } from 'react-router-dom'
import { LayoutGrid } from 'lucide-react'
import { LoginWidget } from '../widgets/auth/LoginWidget'
import { RegisterWidget } from '../widgets/auth/RegisterWidget'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
export function AuthPage() {
  const [searchParams] = useSearchParams()
  const navigate = useNavigate()
  const inviteToken = searchParams.get('invite')

  return (
    <div className="app-shell app-shell--fill relative flex min-h-0 flex-1 flex-col items-center justify-center p-4 gap-8">
      <div className="app-shell-content flex flex-col items-center justify-center gap-8 w-full">
        <div className="flex items-center gap-3 text-foreground">
          <LayoutGrid className="h-6 w-6 text-primary drop-shadow-[0_0_12px_oklch(0.72_0.16_195/0.5)]" />
          <span className="text-2xl font-semibold font-display tracking-[0.2em] uppercase">node-stats</span>
        </div>
        <Card className="w-full max-w-sm">
          <CardHeader className="pb-4">
            <CardTitle className="text-base">
              {inviteToken ? 'Create account' : 'Sign in'}
            </CardTitle>
            <CardDescription>
              {inviteToken
                ? 'You were invited. Use the pre-filled email and create a password.'
                : 'Enter your credentials to continue'}
            </CardDescription>
          </CardHeader>
          <CardContent>
            {inviteToken ? (
              <RegisterWidget
                inviteToken={inviteToken}
                onSwitchToLogin={() => navigate('/auth', { replace: true })}
              />
            ) : (
              <LoginWidget />
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
