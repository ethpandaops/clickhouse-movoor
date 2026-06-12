import { useContext } from 'react';
import { ThemeContext, type ThemeContextValue } from '@/contexts/ThemeContext';

/**
 * Hook to access the current theme mode, resolved theme, and theme-switching functions.
 *
 * Must be used within a ThemeProvider.
 *
 * @returns The current theme context value with mode, resolved theme, and setThemeMode
 * @throws Error if used outside of ThemeProvider
 */
export function useTheme(): ThemeContextValue {
  const context = useContext(ThemeContext);
  if (!context) {
    throw new Error('useTheme must be used within ThemeProvider');
  }
  return context;
}
