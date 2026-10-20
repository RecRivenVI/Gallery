/*
 * 主题与密度。
 *
 * 主题（浅/深/高对比）在两套界面之间**共享**：同一个人在同一台设备上看到的应当是同一套
 * 视觉，因此存储键只有一个 `gallery.theme`。
 *
 * 密度按界面**分离**：管理端默认 compact、画廊默认 comfortable，两者共用一个键会让用户在
 * 管理端选择的紧凑行高漏进画廊。因此存储键是 `gallery.density.<surface>`。
 */

import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';

/** 用户可选择的主题。`system` 表示跟随操作系统偏好。 */
export type ThemePreference = 'system' | 'light' | 'dark' | 'high-contrast';

/** 实际生效的主题。`system` 会解析成 light 或 dark。 */
export type ResolvedTheme = 'light' | 'dark' | 'high-contrast';

export type Density = 'comfortable' | 'compact';

/** 界面身份。决定密度默认值与 :root 上的 data-surface。 */
export type Surface = 'gallery' | 'manage';

const THEME_STORAGE_KEY = 'gallery.theme';
const THEME_VALUES: readonly ThemePreference[] = ['system', 'light', 'dark', 'high-contrast'];
const DENSITY_VALUES: readonly Density[] = ['comfortable', 'compact'];

const DEFAULT_DENSITY: Record<Surface, Density> = {
  gallery: 'comfortable',
  manage: 'compact'
};

export interface ThemeValue {
  theme: ThemePreference;
  setTheme: (theme: ThemePreference) => void;
  /** system 已解析后的主题，供需要按明暗分支的组件使用（例如内嵌图表底色）。 */
  resolvedTheme: ResolvedTheme;
  density: Density;
  setDensity: (density: Density) => void;
  /** 用户在系统层要求减少动效。逻辑动效（自动滚动、自动轮播）必须据此关闭。 */
  reducedMotion: boolean;
  surface: Surface;
}

const ThemeContext = createContext<ThemeValue | null>(null);

function densityStorageKey(surface: Surface): string {
  return `gallery.density.${surface}`;
}

/** localStorage 在隐私模式或被禁用时会抛异常，读写一律兜底成「没有偏好」。 */
function readStored<T extends string>(key: string, allowed: readonly T[]): T | undefined {
  try {
    const raw = localStorage.getItem(key);
    return allowed.find((value) => value === raw);
  } catch {
    return undefined;
  }
}

function writeStored(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {
    // 偏好写不进去不影响本次会话的显示效果，静默忽略。
  }
}

function matches(query: string): boolean {
  return typeof matchMedia === 'function' && matchMedia(query).matches;
}

/** 把偏好解析成实际主题。只有 `system` 需要看系统偏好。 */
export function resolveTheme(theme: ThemePreference): ResolvedTheme {
  if (theme !== 'system') return theme;
  return matches('(prefers-color-scheme: dark)') ? 'dark' : 'light';
}

/**
 * 把主题写到 :root。
 *
 * `system` 必须**移除** data-theme，而不是写成 data-theme="system"：tokens.css 的深色媒体
 * 查询作用于 `:root:not([data-theme])`，留着属性会让系统深色偏好完全失效。
 */
function applyTheme(theme: ThemePreference): void {
  const root = document.documentElement;
  if (theme === 'system') delete root.dataset.theme;
  else root.dataset.theme = theme;
}

export interface ThemeProviderProps {
  surface: Surface;
  children: ReactNode;
}

export function ThemeProvider({ surface, children }: ThemeProviderProps) {
  const [theme, setThemeState] = useState<ThemePreference>(
    () => readStored(THEME_STORAGE_KEY, THEME_VALUES) ?? 'system'
  );
  const [density, setDensityState] = useState<Density>(
    () => readStored(densityStorageKey(surface), DENSITY_VALUES) ?? DEFAULT_DENSITY[surface]
  );
  const [resolvedTheme, setResolvedTheme] = useState<ResolvedTheme>(() => resolveTheme(theme));
  const [reducedMotion, setReducedMotion] = useState<boolean>(() =>
    matches('(prefers-reduced-motion: reduce)')
  );

  useEffect(() => {
    applyTheme(theme);
    setResolvedTheme(resolveTheme(theme));
    if (theme !== 'system' || typeof matchMedia !== 'function') return;
    // 只有 system 模式需要跟随系统切换；显式选择时监听会导致主题被系统改回去。
    const query = matchMedia('(prefers-color-scheme: dark)');
    const onChange = () => setResolvedTheme(query.matches ? 'dark' : 'light');
    query.addEventListener('change', onChange);
    return () => query.removeEventListener('change', onChange);
  }, [theme]);

  useEffect(() => {
    document.documentElement.dataset.density = density;
  }, [density]);

  useEffect(() => {
    document.documentElement.dataset.surface = surface;
  }, [surface]);

  useEffect(() => {
    if (typeof matchMedia !== 'function') return;
    const query = matchMedia('(prefers-reduced-motion: reduce)');
    const onChange = () => setReducedMotion(query.matches);
    query.addEventListener('change', onChange);
    return () => query.removeEventListener('change', onChange);
  }, []);

  const setTheme = useCallback((next: ThemePreference) => {
    setThemeState(next);
    writeStored(THEME_STORAGE_KEY, next);
  }, []);

  const setDensity = useCallback(
    (next: Density) => {
      setDensityState(next);
      writeStored(densityStorageKey(surface), next);
    },
    [surface]
  );

  const value = useMemo<ThemeValue>(
    () => ({ theme, setTheme, resolvedTheme, density, setDensity, reducedMotion, surface }),
    [theme, setTheme, resolvedTheme, density, setDensity, reducedMotion, surface]
  );

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme(): ThemeValue {
  const value = useContext(ThemeContext);
  if (!value) throw new Error('ThemeProvider 缺失');
  return value;
}

/** 主题选择器的中文标签。两端的设置入口共用，避免出现两套叫法。 */
export const THEME_LABELS: Record<ThemePreference, string> = {
  system: '跟随系统',
  light: '浅色',
  dark: '深色',
  'high-contrast': '高对比'
};

export const DENSITY_LABELS: Record<Density, string> = {
  comfortable: '宽松',
  compact: '紧凑'
};
