export type Theme = 'light' | 'dark';
export type ThemeMode = Theme | 'system';

export interface ThemeContextValue {
  mode: ThemeMode;
  theme: Theme;
  setThemeMode: (mode: ThemeMode) => void;
  setTheme: (theme: Theme) => void;
  clearTheme: () => void;
}
