import { type JSX, useEffect, useState, useMemo, useCallback } from 'react';
import { ThemeContext, type Theme, type ThemeMode } from '@/contexts/ThemeContext';

interface ThemeProviderProps {
  children: React.ReactNode;
}

function getSystemTheme(): Theme {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
    return 'light';
  }

  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

function getStoredThemeMode(): ThemeMode {
  if (typeof window === 'undefined') return 'system';

  const stored = localStorage.getItem('theme') as ThemeMode | null;
  if (stored === 'light' || stored === 'dark' || stored === 'system') {
    return stored;
  }

  return 'system';
}

export function ThemeProvider({ children }: ThemeProviderProps): JSX.Element {
  const [mode, setMode] = useState<ThemeMode>(getStoredThemeMode);
  const [systemTheme, setSystemTheme] = useState<Theme>(getSystemTheme);
  const theme = mode === 'system' ? systemTheme : mode;

  // Keep system theme in sync. Explicit user preference always takes priority.
  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return;

    const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)');
    const handleChange = (event: MediaQueryListEvent): void => {
      setSystemTheme(event.matches ? 'dark' : 'light');
    };

    setSystemTheme(mediaQuery.matches ? 'dark' : 'light');

    if (typeof mediaQuery.addEventListener === 'function') {
      mediaQuery.addEventListener('change', handleChange);
      return () => mediaQuery.removeEventListener('change', handleChange);
    }

    mediaQuery.addListener(handleChange);
    return () => mediaQuery.removeListener(handleChange);
  }, []);

  // Persist selected mode.
  useEffect(() => {
    if (typeof window === 'undefined') return;

    localStorage.setItem('theme', mode);
  }, [mode]);

  // Apply theme to document and update theme-color meta tag
  useEffect(() => {
    const root = document.documentElement;
    root.classList.remove('dark');
    if (theme === 'dark') {
      root.classList.add('dark');
    }

    const meta = document.querySelector('meta[name="theme-color"]');
    if (meta) {
      const browserColor = getComputedStyle(root).getPropertyValue('--theme-browser-color').trim();
      if (browserColor) {
        meta.setAttribute('content', browserColor);
      }
    }
  }, [theme]);

  const setThemeMode = useCallback((newMode: ThemeMode): void => {
    setMode(newMode);
  }, []);

  const value = useMemo(() => ({ mode, theme, setThemeMode }), [mode, theme, setThemeMode]);

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}
