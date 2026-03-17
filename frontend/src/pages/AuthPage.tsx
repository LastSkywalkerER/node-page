import { LayoutGrid } from 'lucide-react'
import { LoginWidget } from '../widgets/auth/LoginWidget'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

export function AuthPage() {
  return (
    <div className="min-h-screen flex flex-col items-center justify-center bg-background p-4 gap-6">
      <div className="flex items-center gap-2 text-foreground/80">
        <LayoutGrid className="h-5 w-5" />
        <span className="text-xl font-semibold tracking-tight">node-stats</span>
      </div>
      <Card className="w-full max-w-sm shadow-lg">
        <CardHeader className="pb-4">
          <CardTitle className="text-base">Sign in</CardTitle>
          <CardDescription>Enter your credentials to continue</CardDescription>
        </CardHeader>
        <CardContent>
          <LoginWidget />
        </CardContent>
      </Card>
    </div>
  )
}
