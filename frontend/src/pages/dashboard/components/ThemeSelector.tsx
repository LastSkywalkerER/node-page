import { useThemeQuery, useSetThemeMutation } from '@/shared/hooks/theme';
import type { ThemeType } from '@/shared/types/metrics';
import { Button } from '@/shared/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/shared/ui/dropdown-menu';
import { cn } from '@/shared/lib/utils';

// Props no longer needed as theme is fetched internally

const themes: { value: ThemeType; label: string; description: string }[] = [
  {
    value: 'glass-aurora',
    label: 'Glass Aurora',
    description: 'Glass cards with aurora background',
  },
  {
    value: 'neon-terminal',
    label: 'Neon Terminal',
    description: 'Dark terminal with neon accents',
  },
  {
    value: 'slate-pro',
    label: 'Slate Pro',
    description: 'Professional dark theme',
  },
  {
    value: 'cards-flow',
    label: 'Cards Flow',
    description: 'Mobile-first card layout',
  },
];

const themeButtonStyles: Record<ThemeType, string> = {
  'glass-aurora': 'bg-white/10 border-white/20 text-white hover:bg-white/20 backdrop-blur-sm',
  'neon-terminal': 'bg-green-500/20 border-green-500/30 text-green-400 hover:bg-green-500/30 font-mono',
  'slate-pro': 'bg-slate-700/50 border-slate-600 text-slate-200 hover:bg-slate-600/50',
  'cards-flow': 'bg-blue-100/80 border-blue-200 text-slate-700 hover:bg-blue-200/80',
};

export default function ThemeSelector() {
  const currentTheme = useThemeQuery();
  const setThemeMutation = useSetThemeMutation();

  const currentThemeData = themes.find((t) => t.value === currentTheme);

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          className={cn("justify-between", themeButtonStyles[currentTheme])}
        >
          {currentThemeData?.label}
          <span className="ml-2">▼</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56">
        {themes.map((theme) => (
          <DropdownMenuItem
            key={theme.value}
            onClick={() => setThemeMutation.mutate(theme.value)}
            className={currentTheme === theme.value ? 'bg-accent' : ''}
          >
            <div>
              <div className="font-medium">{theme.label}</div>
              <div className="text-xs text-muted-foreground">{theme.description}</div>
            </div>
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
