// Types
export type {
  WidgetThemeConfig,
  LayoutThemeConfig,
  WidgetType,
  GlobalThemeType
} from './types';

// Hooks
export {
  useWidgetTheme,
  useLayoutTheme,
  useSecondaryText
} from '@/shared/hooks/themes';

// Theme configurations
export { widgetThemes, layoutThemes } from '@/shared/hooks/themes';

// Individual themes (for advanced usage)
export { glassAuroraWidgetThemes, glassAuroraLayoutTheme } from './glass-aurora';
export { neonTerminalWidgetThemes, neonTerminalLayoutTheme } from './neon-terminal';
export { slateProWidgetThemes, slateProLayoutTheme } from './slate-pro';
export { cardsFlowWidgetThemes, cardsFlowLayoutTheme } from './cards-flow';
