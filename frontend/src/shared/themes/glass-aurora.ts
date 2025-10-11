import { WidgetThemeConfig, LayoutThemeConfig, WidgetType } from './types';

export const glassAuroraWidgetThemes: Record<WidgetType, WidgetThemeConfig> = {
  cpu: {
    container: { className: 'text-white' },
    icon: { className: 'bg-white/10' },
    title: { className: 'text-white font-medium' },
    value: { className: 'text-2xl font-bold text-white' },
    chart: { type: 'line', color: '#ff6a5d' },
    details: { show: true }
  },
  memory: {
    container: { className: 'text-white' },
    icon: { className: 'bg-white/10' },
    title: { className: 'text-white font-medium' },
    value: { className: 'text-2xl font-bold text-white' },
    chart: { type: 'line', color: '#b388ff' },
    details: { show: true }
  },
  disk: {
    container: { className: 'text-white' },
    icon: { className: 'bg-white/10' },
    title: { className: 'text-white font-medium' },
    value: { className: 'text-2xl font-bold text-white' },
    chart: { type: 'line', color: '#ffd166' },
    details: { show: true }
  },
  network: {
    container: { className: 'text-white' },
    icon: { className: 'bg-white/10' },
    title: { className: 'text-white font-medium' },
    value: { className: 'text-2xl font-bold text-white' },
    chart: { type: 'line', color: '#2dd4bf' },
    details: { show: true }
  },
  docker: {
    container: { className: 'text-white' },
    icon: { className: 'bg-white/10' },
    title: { className: 'text-lg font-semibold text-white mb-4' },
    value: { className: 'text-2xl font-bold text-white' },
    chart: { type: 'line', color: '#22c55e' },
    details: { show: true }
  },
  'system-health': {
    container: { className: 'text-white' },
    icon: { className: 'bg-white/10' },
    title: { className: 'text-white font-medium' },
    value: { className: 'text-2xl font-bold text-white' },
    chart: { type: 'area', color: '#10b981', fill: 'rgba(16, 185, 129, 0.1)' },
    details: { show: true, className: 'text-sm opacity-70' }
  },
  hosts: {
    container: { className: 'text-white' },
    icon: { className: 'bg-white/10' },
    title: { className: 'text-lg font-semibold text-white/90' },
    value: { className: 'text-white' },
    chart: { type: 'line', color: '#60a5fa' },
    details: { show: true, className: 'text-white/60' }
  },
  sensors: {
    container: { className: 'text-white' },
    icon: { className: 'bg-white/10' },
    title: { className: 'text-white font-medium' },
    value: { className: 'text-2xl font-bold text-white' },
    chart: { type: 'line', color: '#f87171' },
    details: { show: true, className: 'text-white/70' }
  }
};

export const glassAuroraLayoutTheme: LayoutThemeConfig = {
  mainContainer: { className: 'space-y-6' },
  card: { className: 'glass rounded-xl p-6' },
  chartContainer: { className: 'glass rounded-xl p-6' },
  heading: { className: 'text-lg font-semibold text-white mb-4' },
  subheading: { className: 'text-xs text-white/60 uppercase tracking-wide' },
  body: { className: 'text-sm text-white' },
  secondaryText: { className: 'text-white/60' },
  hostItem: {
    className: 'glass rounded-lg p-3 cursor-pointer transition-all duration-200 hover:bg-white/5 border border-white/10',
    selectedClassName: 'bg-white/10 border-white/20 ring-2 ring-blue-400/50',
  },
  chart: {
    gridColor: 'rgba(255,255,255,0.1)',
    axisColor: 'rgba(255,255,255,0.6)',
    tooltip: {
      backgroundColor: 'rgba(0,0,0,0.8)',
      borderColor: 'rgba(255,255,255,0.1)',
      textColor: 'white',
    },
  },
  skeleton: { className: 'glass rounded-xl p-6' },
};
