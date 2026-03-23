import { useEffect } from 'react'
import { Routes, Route, Navigate, Outlet } from 'react-router-dom'
import { useUserStore } from './shared/store/user'
import { initTheme } from './shared/hooks/useTheme'
import { ProtectedRoute } from './shared/guards/ProtectedRoute'
import { SetupRoute } from './shared/guards/SetupRoute'
import { AppHeader } from './shared/components/AppHeader'
import { AuthPage } from './pages/AuthPage'
import { SetupPage } from './pages/SetupPage'
import { MachineListPage } from './pages/MachineListPage'
import { MachineStatsPage } from './pages/MachineStatsPage'
import { MachineContainersPage } from './pages/MachineContainersPage'
import { AdminPage } from './pages/AdminPage'
import { UsersTab } from './widgets/admin/UsersTab'
import { NodesTab } from './widgets/admin/NodesTab'
import { AdminRoute } from './shared/guards/AdminRoute'

function ProtectedLayout() {
  return (
    <SetupRoute>
      <ProtectedRoute>
        <div className="min-h-screen bg-background">
          <AppHeader />
          <main>
            <Outlet />
          </main>
        </div>
      </ProtectedRoute>
    </SetupRoute>
  )
}

function App() {
  const { initializeAuth, isAuthenticated } = useUserStore()

  useEffect(() => {
    initTheme()
    initializeAuth()
  }, [initializeAuth])

  return (
    <Routes>
      <Route path="/setup" element={<SetupPage />} />

      <Route
        path="/auth"
        element={
          <SetupRoute>
            {isAuthenticated ? <Navigate to="/machines" replace /> : <AuthPage />}
          </SetupRoute>
        }
      />

      <Route element={<ProtectedLayout />}>
        <Route path="/machines" element={<MachineListPage />} />
        <Route path="/machines/:id/stats" element={<MachineStatsPage />} />
        <Route path="/machines/:id/containers" element={<MachineContainersPage />} />
        <Route element={<AdminRoute />}>
          <Route path="/admin" element={<AdminPage />}>
            <Route index element={<Navigate to="users" replace />} />
            <Route path="users" element={<UsersTab />} />
            <Route path="nodes" element={<NodesTab />} />
          </Route>
        </Route>
      </Route>

      <Route
        path="*"
        element={
          <SetupRoute>
            <Navigate to={isAuthenticated ? '/machines' : '/auth'} replace />
          </SetupRoute>
        }
      />
    </Routes>
  )
}

export default App
