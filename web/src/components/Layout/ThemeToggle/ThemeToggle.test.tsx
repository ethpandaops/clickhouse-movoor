import { describe, expect, it, beforeEach, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { ThemeToggle } from './ThemeToggle';

const themeMocks = vi.hoisted(() => ({
  mode: 'light' as 'light' | 'dark' | 'system',
  theme: 'light' as 'light' | 'dark',
  setThemeMode: vi.fn(),
  setTheme: vi.fn(),
  clearTheme: vi.fn(),
}));

vi.mock('@/hooks/useTheme', () => ({
  useTheme: () => ({
    mode: themeMocks.mode,
    theme: themeMocks.theme,
    setThemeMode: themeMocks.setThemeMode,
    setTheme: themeMocks.setTheme,
    clearTheme: themeMocks.clearTheme,
  }),
}));

describe('ThemeToggle', () => {
  beforeEach(() => {
    themeMocks.mode = 'light';
    themeMocks.theme = 'light';
    themeMocks.setThemeMode.mockReset();
  });

  it('renders a button with the current theme label', () => {
    render(<ThemeToggle />);

    const button = screen.getByRole('button');
    expect(button).toHaveAccessibleName(/light theme/i);
  });

  it('cycles from light to dark on click', () => {
    render(<ThemeToggle />);

    fireEvent.click(screen.getByRole('button'));

    expect(themeMocks.setThemeMode).toHaveBeenCalledWith('dark');
  });

  it('cycles from dark to system on click', () => {
    themeMocks.mode = 'dark';
    render(<ThemeToggle />);

    fireEvent.click(screen.getByRole('button'));

    expect(themeMocks.setThemeMode).toHaveBeenCalledWith('system');
  });

  it('cycles from system back to light on click', () => {
    themeMocks.mode = 'system';
    render(<ThemeToggle />);

    fireEvent.click(screen.getByRole('button'));

    expect(themeMocks.setThemeMode).toHaveBeenCalledWith('light');
  });
});
