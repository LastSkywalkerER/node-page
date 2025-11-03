import { useEffect } from 'react';
import { Routes, Route, Navigate } from 'react-router-dom';
import { useUserStore } from './shared/store/user';
import { useThemeQuery } from './shared/hooks/theme';
import { ProtectedRoute } from './shared/guards/ProtectedRoute';
import { AuthPage } from './pages/AuthPage';
import Dashboard from './pages/dashboard/Dashboard';
import { DashboardLayout } from './pages/dashboard/DashboardLayout';
import { HeaderBar } from './shared/components/HeaderBar';

/**
 * App is the main React component that serves as the root of the application.
 * This component manages routing, authentication, and theme switching.
 */
function App() {
  const theme = useThemeQuery();
  const { initializeFromStorage, isAuthenticated } = useUserStore();

  useEffect(() => {
    // Initialize user authentication state from storage (sync, no API calls)
    // Actual API verification happens in ProtectedRoute via useEnsureAuth
    initializeFromStorage();
  }, [initializeFromStorage]);

  useEffect(() => {
    // Apply theme-specific styles to document root for CSS variable theming
    document.documentElement.setAttribute('data-theme', theme);

    // Apply theme-specific Tailwind CSS classes to body element
    // Each theme has a unique background gradient or solid color
    const bodyClasses = {
      'glass-aurora': 'bg-[radial-gradient(80%_80%_at_20%_10%,#3b1e94_0%,#1b1b3a_45%,#101421_100%)]',
      'neon-terminal': 'bg-[#0b0f12]',
      'slate-pro': 'bg-slate-950',
      'cards-flow': 'bg-gradient-to-b from-[#0f1120] to-[#0a0c18]',
    };

    // Apply the selected theme's background class or default to glass-aurora
    document.body.className = bodyClasses[theme] || '';
  }, [theme]);

  return (
    <div className="min-h-screen text-white">
      {/* Header bar shown only when authenticated */}
      {isAuthenticated && <HeaderBar />}

      <Routes>
        {/* Public auth route */}
        <Route
          path="/auth"
          element={
            isAuthenticated ? <Navigate to="/dashboard" replace /> : <AuthPage />
          }
        />

        {/* Protected dashboard routes */}
        <Route
          path="/dashboard/*"
          element={
            <ProtectedRoute>
              <Dashboard>
                <DashboardLayout />
              </Dashboard>
            </ProtectedRoute>
          }
        />

        {/* Default redirect */}
        <Route
          path="/"
          element={<Navigate to={isAuthenticated ? "/dashboard" : "/auth"} replace />}
        />

        {/* Catch all - redirect to dashboard or auth */}
        <Route
          path="*"
          element={<Navigate to={isAuthenticated ? "/dashboard" : "/auth"} replace />}
        />
      </Routes>
    </div>
  );
}

export default App;
