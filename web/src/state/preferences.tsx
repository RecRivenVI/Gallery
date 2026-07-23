import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';

export type Theme = 'system' | 'light' | 'dark';
type Preferences = {
  theme: Theme;
  setTheme: (theme: Theme) => void;
  sidebarOpen: boolean;
  setSidebarOpen: (open: boolean) => void;
};

const PreferencesContext = createContext<Preferences | null>(null);

function readTheme(): Theme {
  const value = localStorage.getItem('gallery.theme');
  return value === 'light' || value === 'dark' ? value : 'system';
}

export function PreferencesProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(readTheme);
  const [sidebarOpen, setSidebarOpenState] = useState(
    () => localStorage.getItem('gallery.sidebar') !== 'closed'
  );

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    localStorage.setItem('gallery.theme', theme);
  }, [theme]);

  const value = useMemo<Preferences>(
    () => ({
      theme,
      setTheme: setThemeState,
      sidebarOpen,
      setSidebarOpen(open) {
        setSidebarOpenState(open);
        localStorage.setItem('gallery.sidebar', open ? 'open' : 'closed');
      }
    }),
    [theme, sidebarOpen]
  );
  return <PreferencesContext.Provider value={value}>{children}</PreferencesContext.Provider>;
}

export function usePreferences(): Preferences {
  const value = useContext(PreferencesContext);
  if (!value) throw new Error('PreferencesProvider 缺失');
  return value;
}
