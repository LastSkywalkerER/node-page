import { useEffect } from 'react';
import { useThemeQuery } from '@/shared/hooks/theme';
import Dashboard from '@/pages/dashboard/Dashboard';
import { DashboardLayout } from '@/pages/dashboard/DashboardLayout';

/**
 * App is the main React component that serves as the root of the application.
 * This component manages theme switching and renders the dashboard with the selected theme.
 * It applies theme-specific styles to the document and body elements.
 *
 * @returns {JSX.Element} The main application component with theme-aware dashboard
 */
function App() {
  const theme = useThemeQuery();

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

  /**
   * renderDashboard renders the universal dashboard layout component.
   * The layout automatically adapts to the selected theme using visual theming.
   *
   * @returns {JSX.Element} The universal dashboard component
   */
  const renderDashboard = () => {
    return <DashboardLayout />;
  };

  return (
    <div className="min-h-screen text-white">
      <Dashboard>
        {renderDashboard()}
      </Dashboard>
    </div>
  );
}

export default App;
