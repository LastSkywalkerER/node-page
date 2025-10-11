import { WidgetThemeConfig, LayoutThemeConfig, WidgetType } from './types';

export const slateProWidgetThemes: Record<WidgetType, WidgetThemeConfig> = {
  cpu: {
    container: { className: 'text-slate-100' },
    icon: { className: 'bg-slate-700/50' },
    title: { className: 'text-slate-200 font-medium' },
    value: { className: 'text-2xl font-bold text-white' },
    chart: { type: 'line', color: '#60a5fa' },
    details: { show: true, className: 'text-slate-400 text-sm' }
  },
  memory: {
    container: { className: 'text-slate-100' },
    icon: { className: 'bg-slate-700/50' },
    title: { className: 'text-slate-200 font-medium' },
    value: { className: 'text-2xl font-bold text-white' },
    chart: { type: 'line', color: '#60a5fa' },
    details: { show: true, className: 'text-slate-400 text-sm' }
  },
  disk: {
    container: { className: 'text-slate-100' },
    icon: { className: 'bg-slate-700/50' },
    title: { className: 'text-slate-200 font-medium' },
    value: { className: 'text-2xl font-bold text-white' },
    chart: { type: 'line', color: '#60a5fa' },
    details: { show: true, className: 'text-slate-400 text-sm' }
  },
  network: {
    container: { className: 'text-slate-100' },
    icon: { className: 'bg-slate-700/50' },
    title: { className: 'text-slate-200 font-medium' },
    value: { className: 'text-2xl font-bold text-white' },
    chart: { type: 'line', color: '#60a5fa' },
    details: { show: true, className: 'text-slate-400 text-sm' }
  },
  docker: {
    container: { className: 'text-slate-100' },
    icon: { className: 'bg-slate-700/50' },
    title: { className: 'text-lg font-semibold text-slate-200 mb-4' },
    value: { className: 'text-2xl font-bold text-white' },
    chart: { type: 'line', color: '#60a5fa' },
    details: { show: true }
  },
  'system-health': {
    container: { className: 'text-slate-100' },
    icon: { className: 'bg-slate-700/50' },
    title: { className: 'text-slate-200 font-medium' },
    value: { className: 'text-2xl font-bold text-white' },
    chart: { type: 'area', color: '#10b981', fill: 'rgba(16, 185, 129, 0.1)' },
    details: { show: true, className: 'text-slate-400 text-sm' }
  },
  hosts: {
    container: { className: 'text-slate-100' },
    icon: { className: 'bg-slate-700/50' },
    title: { className: 'text-lg font-semibold text-slate-200' },
    value: { className: 'text-slate-200' },
    chart: { type: 'line', color: '#60a5fa' },
    details: { show: true, className: 'text-slate-400 text-sm' }
  },
  sensors: {
    container: { className: 'text-slate-100' },
    icon: { className: 'bg-slate-700/50' },
    title: { className: 'text-slate-200 font-medium' },
    value: { className: 'text-2xl font-bold text-white' },
    chart: { type: 'line', color: '#f87171' },
    details: { show: true, className: 'text-slate-400 text-sm' }
  }
};

export const slateProLayoutTheme: LayoutThemeConfig = {
  mainContainer: { className: 'space-y-6' },
  card: { className: 'bg-slate-900/70 border border-slate-800 rounded-lg p-4' },
  chartContainer: { className: 'bg-slate-900/70 border border-slate-800 rounded-lg p-6' },
  heading: { className: 'text-lg font-semibold text-slate-100 mb-4' },
  subheading: { className: 'text-xs text-slate-400 uppercase tracking-wide mb-2' },
  body: { className: 'text-sm text-slate-100' },
  secondaryText: { className: 'text-slate-400' },
  hostItem: {
    className: 'bg-slate-900/50 border border-slate-800 rounded-lg p-3 cursor-pointer transition-all duration-200 hover:bg-slate-800/50',
    selectedClassName: 'bg-slate-800/70 border-slate-600 ring-2 ring-blue-400/50',
  },
  chart: {
    gridColor: 'hsl(var(--muted))',
    axisColor: 'hsl(var(--muted-foreground))',
    tooltip: {
      backgroundColor: 'hsl(var(--background))',
      borderColor: 'hsl(var(--border))',
      textColor: 'hsl(var(--foreground))',
    },
  },
  skeleton: { className: 'bg-slate-900/70 border border-slate-800 rounded-lg p-4' },
};
