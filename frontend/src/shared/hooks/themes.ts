import { useThemeQuery } from './theme';
import { WidgetThemeConfig, LayoutThemeConfig, WidgetType } from '../themes/types';
import { glassAuroraWidgetThemes, glassAuroraLayoutTheme } from '../themes/glass-aurora';
import { neonTerminalWidgetThemes, neonTerminalLayoutTheme } from '../themes/neon-terminal';
import { slateProWidgetThemes, slateProLayoutTheme } from '../themes/slate-pro';
import { cardsFlowWidgetThemes, cardsFlowLayoutTheme } from '../themes/cards-flow';

export const widgetThemes: Record<string, Record<WidgetType, WidgetThemeConfig>> = {
  'glass-aurora': glassAuroraWidgetThemes,
  'neon-terminal': neonTerminalWidgetThemes,
  'slate-pro': slateProWidgetThemes,
  'cards-flow': cardsFlowWidgetThemes,
};

export const layoutThemes: Record<string, LayoutThemeConfig> = {
  'glass-aurora': glassAuroraLayoutTheme,
  'neon-terminal': neonTerminalLayoutTheme,
  'slate-pro': slateProLayoutTheme,
  'cards-flow': cardsFlowLayoutTheme,
};

// Hook to get widget theme
export function useWidgetTheme(widgetType: WidgetType) {
  const theme = useThemeQuery();
  return widgetThemes[theme]?.[widgetType] || widgetThemes['glass-aurora'][widgetType];
}

// Hook to get layout theme
export function useLayoutTheme() {
  const theme = useThemeQuery();
  return layoutThemes[theme] || layoutThemes['glass-aurora'];
}

// Hook to get secondary text style
export function useSecondaryText() {
  const layoutTheme = useLayoutTheme();
  return layoutTheme.secondaryText.className;
}
