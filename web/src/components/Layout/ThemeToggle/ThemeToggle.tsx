import type { JSX } from 'react';
import { ComputerDesktopIcon, MoonIcon, SunIcon } from '@heroicons/react/24/solid';
import { useTheme } from '@/hooks/useTheme';
import type { ThemeMode } from '@/contexts/ThemeContext';

const cycle: ThemeMode[] = ['light', 'dark', 'system'];

const config: Record<ThemeMode, { icon: typeof SunIcon; label: string }> = {
  light: { icon: SunIcon, label: 'Light theme' },
  dark: { icon: MoonIcon, label: 'Dark theme' },
  system: { icon: ComputerDesktopIcon, label: 'System theme' },
};

/**
 * Single button that cycles through light → dark → system theme modes.
 */
export function ThemeToggle(): JSX.Element {
  const { mode, setThemeMode } = useTheme();

  const nextMode = cycle[(cycle.indexOf(mode) + 1) % cycle.length] ?? 'light';
  const { icon: Icon, label } = config[mode];

  return (
    <button
      type="button"
      aria-label={`${label} — click for ${config[nextMode].label.toLowerCase()}`}
      title={`${label} — click for ${config[nextMode].label.toLowerCase()}`}
      onClick={() => setThemeMode(nextMode)}
      className="inline-flex items-center justify-center rounded-md p-1.5 text-muted transition-colors hover:bg-accent/10 hover:text-accent focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
    >
      <Icon className="size-4" />
    </button>
  );
}
