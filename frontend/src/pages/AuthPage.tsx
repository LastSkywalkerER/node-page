import { LoginWidget } from '../widgets/auth/LoginWidget';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '../shared/ui/card';

export function AuthPage() {
  return (
    <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-slate-900 via-purple-900 to-slate-900 p-4">
      <div className="w-full max-w-md">
        <div className="text-center mb-8">
          <h1 className="text-4xl font-bold text-white mb-2">System Stats</h1>
          <p className="text-slate-400">Monitor your system performance</p>
        </div>

        <Card className="bg-slate-800/50 border-slate-700">
          <CardHeader className="text-center">
            <CardTitle className="text-white">Welcome</CardTitle>
            <CardDescription className="text-slate-400">
              Sign in to your account
            </CardDescription>
          </CardHeader>
          <CardContent>
            <LoginWidget />
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
