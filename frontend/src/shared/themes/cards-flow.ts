import { WidgetThemeConfig, LayoutThemeConfig, WidgetType } from './types';

export const cardsFlowWidgetThemes: Record<WidgetType, WidgetThemeConfig> = {
  cpu: {
    container: { className: 'text-white' },
    icon: { className: 'bg-slate-800 text-white' },
    title: { className: 'text-white font-medium' },
    value: { className: 'text-2xl font-bold text-white' },
    chart: { type: 'area', color: '#3b82f6', fill: 'rgba(59, 130, 246, 0.1)' },
    details: { show: true, className: 'text-slate-400 text-sm' }
  },
  memory: {
    container: { className: 'text-white' },
    icon: { className: 'bg-slate-800 text-white' },
    title: { className: 'text-white font-medium' },
    value: { className: 'text-2xl font-bold text-white' },
    chart: { type: 'area', color: '#3b82f6', fill: 'rgba(59, 130, 246, 0.1)' },
    details: { show: true, className: 'text-slate-400 text-sm' }
  },
  disk: {
    container: { className: 'text-white' },
    icon: { className: 'bg-slate-800 text-white' },
    title: { className: 'text-white font-medium' },
    value: { className: 'text-2xl font-bold text-white' },
    chart: { type: 'area', color: '#3b82f6', fill: 'rgba(59, 130, 246, 0.1)' },
    details: { show: true, className: 'text-slate-400 text-sm' }
  },
  network: {
    container: { className: 'text-white' },
    icon: { className: 'bg-slate-800 text-white' },
    title: { className: 'text-white font-medium' },
    value: { className: 'text-2xl font-bold text-white' },
    chart: { type: 'area', color: '#3b82f6', fill: 'rgba(59, 130, 246, 0.1)' },
    details: { show: true, className: 'text-slate-400 text-sm' }
  },
  docker: {
    container: { className: 'text-white' },
    icon: { className: 'bg-slate-800 text-white' },
    title: { className: 'text-lg font-semibold text-white mb-4' },
    value: { className: 'text-2xl font-bold text-white' },
    chart: { type: 'area', color: '#3b82f6', fill: 'rgba(59, 130, 246, 0.1)' },
    details: { show: true }
  },
  'system-health': {
    container: { className: 'text-white' },
    icon: { className: 'bg-slate-800 text-white' },
    title: { className: 'text-white font-medium' },
    value: { className: 'text-2xl font-bold text-white' },
    chart: { type: 'area', color: '#10b981', fill: 'rgba(16, 185, 129, 0.1)' },
    details: { show: true, className: 'text-slate-400 text-sm' }
  },
  hosts: {
    container: { className: 'text-white' },
    icon: { className: 'bg-slate-800 text-white' },
    title: { className: 'text-lg font-semibold text-white' },
    value: { className: 'text-white' },
    chart: { type: 'line', color: '#60a5fa' },
    details: { show: true, className: 'text-slate-400 text-sm' }
  },
  sensors: {
    container: { className: 'text-white' },
    icon: { className: 'bg-slate-800 text-white' },
    title: { className: 'text-white font-medium' },
    value: { className: 'text-2xl font-bold text-white' },
    chart: { type: 'area', color: '#ef4444', fill: 'rgba(239, 68, 68, 0.1)' },
    details: { show: true, className: 'text-slate-400 text-sm' }
  }
};

export const cardsFlowLayoutTheme: LayoutThemeConfig = {
  mainContainer: { className: 'space-y-6' },
  card: { className: 'bg-white/5 border border-white/10 rounded-2xl p-6 shadow-xl' },
  chartContainer: { className: 'bg-white/5 border border-white/10 rounded-2xl p-6 shadow-xl' },
  heading: { className: 'text-lg font-semibold text-white mb-4' },
  subheading: { className: 'text-xs text-white/80 uppercase tracking-wide' },
  body: { className: 'text-sm text-white' },
  secondaryText: { className: 'text-white/60' },
  hostItem: {
    className: 'bg-white/5 border border-white/10 rounded-xl p-3 cursor-pointer transition-all duration-200 hover:bg-white/10 shadow-lg',
    selectedClassName: 'bg-white/15 border-white/20 ring-2 ring-blue-400/50 shadow-xl',
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
  skeleton: { className: 'bg-white/5 border border-white/10 rounded-2xl p-6 shadow-xl' },
};
