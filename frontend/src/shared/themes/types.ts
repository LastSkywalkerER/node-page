// Widget themes configuration
// Each widget can have different styles based on the global theme

export interface WidgetThemeConfig {
  container: {
    className: string;
  };
  icon: {
    className: string;
  };
  title: {
    className: string;
  };
  value: {
    className: string;
  };
  chart: {
    type: 'line' | 'area';
    color: string;
    fill?: string;
  };
  details: {
    show: boolean;
    className?: string;
  };
}

export interface LayoutThemeConfig {
  // Main container styles
  mainContainer: {
    className: string;
  };
  // Card/KPI container styles
  card: {
    className: string;
  };
  // Chart container styles
  chartContainer: {
    className: string;
  };
  // Host list item styles
  hostItem: {
    className: string;
    selectedClassName: string;
  };
  // Text styles
  heading: {
    className: string;
  };
  subheading: {
    className: string;
  };
  body: {
    className: string;
  };
  // Secondary text for details and labels
  secondaryText: {
    className: string;
  };
  // Chart specific styles
  chart: {
    gridColor: string;
    axisColor: string;
    tooltip: {
      backgroundColor: string;
      borderColor: string;
      textColor: string;
    };
  };
  // Loading skeleton
  skeleton: {
    className: string;
  };
}

export type WidgetType = 'cpu' | 'memory' | 'disk' | 'network' | 'docker' | 'system-health' | 'hosts' | 'sensors';

export type GlobalThemeType = 'glass-aurora' | 'neon-terminal' | 'slate-pro' | 'cards-flow';
