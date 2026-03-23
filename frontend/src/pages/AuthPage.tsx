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
    <div className="min-h-screen flex flex-col items-center justify-center bg-background p-4 gap-6">
      <div className="flex items-center gap-2 text-foreground/80">
        <LayoutGrid className="h-5 w-5" />
        <span className="text-xl font-semibold tracking-tight">node-stats</span>
      </div>
      <Card className="w-full max-w-sm shadow-lg">
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
  )
}
