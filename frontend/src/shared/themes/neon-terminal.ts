import { WidgetThemeConfig, LayoutThemeConfig, WidgetType } from './types';

export const neonTerminalWidgetThemes: Record<WidgetType, WidgetThemeConfig> = {
  cpu: {
    container: { className: 'text-green-400' },
    icon: { className: 'bg-green-500/20 border border-green-500/30' },
    title: { className: 'text-green-400 font-mono' },
    value: { className: 'text-2xl font-bold text-green-300' },
    chart: { type: 'area', color: '#00ff88', fill: 'rgba(0, 255, 136, 0.1)' },
    details: { show: true, className: 'text-green-600 text-xs font-mono' }
  },
  memory: {
    container: { className: 'text-green-400' },
    icon: { className: 'bg-green-500/20 border border-green-500/30' },
    title: { className: 'text-green-400 font-mono' },
    value: { className: 'text-2xl font-bold text-green-300' },
    chart: { type: 'area', color: '#00ff88', fill: 'rgba(0, 255, 136, 0.1)' },
    details: { show: true, className: 'text-green-600 text-xs font-mono' }
  },
  disk: {
    container: { className: 'text-green-400' },
    icon: { className: 'bg-green-500/20 border border-green-500/30' },
    title: { className: 'text-green-400 font-mono' },
    value: { className: 'text-2xl font-bold text-green-300' },
    chart: { type: 'area', color: '#00ff88', fill: 'rgba(0, 255, 136, 0.1)' },
    details: { show: true, className: 'text-green-600 text-xs font-mono' }
  },
  network: {
    container: { className: 'text-green-400' },
    icon: { className: 'bg-green-500/20 border border-green-500/30' },
    title: { className: 'text-green-400 font-mono' },
    value: { className: 'text-2xl font-bold text-green-300' },
    chart: { type: 'area', color: '#00ff88', fill: 'rgba(0, 255, 136, 0.1)' },
    details: { show: true, className: 'text-green-600 text-xs font-mono' }
  },
  docker: {
    container: { className: 'text-green-400' },
    icon: { className: 'bg-green-500/20 border border-green-500/30' },
    title: { className: 'text-lg font-semibold text-green-400 mb-4 font-mono' },
    value: { className: 'text-2xl font-bold text-green-300' },
    chart: { type: 'area', color: '#00ff88', fill: 'rgba(0, 255, 136, 0.1)' },
    details: { show: true }
  },
  'system-health': {
    container: { className: 'text-green-400' },
    icon: { className: 'bg-green-500/20 border border-green-500/30' },
    title: { className: 'text-green-400 font-mono' },
    value: { className: 'text-2xl font-bold text-green-300' },
    chart: { type: 'area', color: '#00ff88', fill: 'rgba(0, 255, 136, 0.1)' },
    details: { show: true, className: 'text-green-600 text-xs font-mono' }
  },
  hosts: {
    container: { className: 'text-green-400' },
    icon: { className: 'bg-green-500/20 border border-green-500/30' },
    title: { className: 'text-lg font-semibold text-green-400 font-mono' },
    value: { className: 'text-green-400' },
    chart: { type: 'line', color: '#00ff88' },
    details: { show: true, className: 'text-green-600 text-xs font-mono' }
  }
};

export const neonTerminalLayoutTheme: LayoutThemeConfig = {
  mainContainer: { className: 'grid grid-cols-24 gap-4 font-mono' },
  card: { className: 'bg-[#0f1419] border border-[#1b2530] rounded-lg p-4' },
  chartContainer: { className: 'bg-[#0f1419] border border-[#1b2530] rounded-lg p-4' },
  heading: { className: 'text-[#00ff95] font-bold mb-4' },
  subheading: { className: 'text-[#41ead4] font-mono text-xs' },
  body: { className: 'text-white font-mono text-sm' },
  secondaryText: { className: 'text-[#41ead4]/70' },
  hostItem: {
    className: 'bg-[#0f1419] border border-[#1b2530] rounded-lg p-3 cursor-pointer transition-all duration-200 hover:bg-[#1b2530] font-mono',
    selectedClassName: 'bg-[#1b2530] border-[#41ead4] ring-2 ring-[#00ff95]/50',
  },
  chart: {
    gridColor: '#1b2530',
    axisColor: '#41ead4',
    tooltip: {
      backgroundColor: '#0f1419',
      borderColor: '#1b2530',
      textColor: 'white',
    },
  },
  skeleton: { className: 'bg-[#0f1419] border border-[#1b2530] rounded-lg p-4' },
};
