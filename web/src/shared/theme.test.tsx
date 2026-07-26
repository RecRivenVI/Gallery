/*
 * 主题与密度的行为契约。
 *
 * 断言的是写到 :root 上的属性与持久化行为——那是 tokens.css 的层叠唯一依赖的输入。
 */

import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ThemeProvider, resolveTheme, useTheme, type ThemePreference } from './theme';

type MediaListener = () => void;

/** 可控的 matchMedia 桩。jsdom 没有实现它，而主题解析与减少动效都依赖它。 */
function stubMatchMedia(matchedQueries: Set<string>) {
  const listeners = new Map<string, Set<MediaListener>>();
  vi.stubGlobal('matchMedia', (query: string) => ({
    media: query,
    // 必须是 getter：ThemeProvider 会持有 MediaQueryList 并在 change 回调里重新读 matches，
    // 写成快照布尔值会让「跟随系统切换」永远读到创建时的旧值。
    get matches() {
      return matchedQueries.has(query);
    },
    addEventListener: (_type: string, listener: MediaListener) => {
      const set = listeners.get(query) ?? new Set<MediaListener>();
      set.add(listener);
      listeners.set(query, set);
    },
    removeEventListener: (_type: string, listener: MediaListener) => {
      listeners.get(query)?.delete(listener);
    },
    addListener: () => undefined,
    removeListener: () => undefined,
    onchange: null,
    dispatchEvent: () => false
  }));
  return {
    set(query: string, matches: boolean) {
      if (matches) matchedQueries.add(query);
      else matchedQueries.delete(query);
      act(() => {
        for (const listener of listeners.get(query) ?? []) listener();
      });
    }
  };
}

function Probe() {
  const { theme, setTheme, resolvedTheme, density, setDensity, reducedMotion, surface } = useTheme();
  return (
    <div>
      <span data-testid="theme">{theme}</span>
      <span data-testid="resolved">{resolvedTheme}</span>
      <span data-testid="density">{density}</span>
      <span data-testid="surface">{surface}</span>
      <span data-testid="reduced-motion">{String(reducedMotion)}</span>
      {(['system', 'light', 'dark', 'high-contrast'] as ThemePreference[]).map((value) => (
        <button key={value} onClick={() => setTheme(value)}>
          {value}
        </button>
      ))}
      <button onClick={() => setDensity('compact')}>compact</button>
      <button onClick={() => setDensity('comfortable')}>comfortable</button>
    </div>
  );
}

beforeEach(() => {
  localStorage.clear();
  delete document.documentElement.dataset.theme;
  delete document.documentElement.dataset.density;
  delete document.documentElement.dataset.surface;
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('ThemeProvider', () => {
  it('三套显式主题都写到 data-theme 上', async () => {
    stubMatchMedia(new Set());
    render(
      <ThemeProvider surface="gallery">
        <Probe />
      </ThemeProvider>
    );
    for (const theme of ['light', 'dark', 'high-contrast']) {
      await userEvent.click(screen.getByRole('button', { name: theme }));
      expect(document.documentElement.dataset.theme).toBe(theme);
      expect(screen.getByTestId('theme')).toHaveTextContent(theme);
      expect(screen.getByTestId('resolved')).toHaveTextContent(theme);
    }
  });

  it('system 模式移除 data-theme，让系统偏好媒体查询重新生效', async () => {
    stubMatchMedia(new Set(['(prefers-color-scheme: dark)']));
    render(
      <ThemeProvider surface="gallery">
        <Probe />
      </ThemeProvider>
    );
    await userEvent.click(screen.getByRole('button', { name: 'dark' }));
    expect(document.documentElement.dataset.theme).toBe('dark');

    await userEvent.click(screen.getByRole('button', { name: 'system' }));
    // 写成 data-theme="system" 会让 tokens.css 的 :root:not([data-theme]) 永不命中，
    // 系统深色偏好因此完全失效——必须是移除属性。
    expect(document.documentElement.dataset.theme).toBeUndefined();
    expect(screen.getByTestId('resolved')).toHaveTextContent('dark');
  });

  it('system 模式跟随系统偏好变化，显式选择不跟随', async () => {
    const media = stubMatchMedia(new Set());
    render(
      <ThemeProvider surface="gallery">
        <Probe />
      </ThemeProvider>
    );
    expect(screen.getByTestId('resolved')).toHaveTextContent('light');
    media.set('(prefers-color-scheme: dark)', true);
    expect(screen.getByTestId('resolved')).toHaveTextContent('dark');

    await userEvent.click(screen.getByRole('button', { name: 'light' }));
    media.set('(prefers-color-scheme: dark)', false);
    media.set('(prefers-color-scheme: dark)', true);
    expect(screen.getByTestId('resolved')).toHaveTextContent('light');
  });

  it('持久化主题选择', async () => {
    stubMatchMedia(new Set());
    const { unmount } = render(
      <ThemeProvider surface="gallery">
        <Probe />
      </ThemeProvider>
    );
    await userEvent.click(screen.getByRole('button', { name: 'high-contrast' }));
    unmount();

    render(
      <ThemeProvider surface="manage">
        <Probe />
      </ThemeProvider>
    );
    // 主题两端共享：管理端应当直接沿用画廊里选的高对比。
    expect(screen.getByTestId('theme')).toHaveTextContent('high-contrast');
  });

  it('密度默认值按界面分离，且互不污染', async () => {
    stubMatchMedia(new Set());
    const gallery = render(
      <ThemeProvider surface="gallery">
        <Probe />
      </ThemeProvider>
    );
    expect(screen.getByTestId('density')).toHaveTextContent('comfortable');
    expect(document.documentElement.dataset.density).toBe('comfortable');
    expect(document.documentElement.dataset.surface).toBe('gallery');
    gallery.unmount();

    const manage = render(
      <ThemeProvider surface="manage">
        <Probe />
      </ThemeProvider>
    );
    expect(screen.getByTestId('density')).toHaveTextContent('compact');
    await userEvent.click(screen.getByRole('button', { name: 'comfortable' }));
    manage.unmount();

    render(
      <ThemeProvider surface="gallery">
        <Probe />
      </ThemeProvider>
    );
    // 管理端改成 comfortable 不应改变画廊自己的记忆。
    expect(screen.getByTestId('density')).toHaveTextContent('comfortable');
    expect(localStorage.getItem('gallery.density.manage')).toBe('comfortable');
  });

  it('暴露系统的减少动效偏好并跟随变化', () => {
    const media = stubMatchMedia(new Set(['(prefers-reduced-motion: reduce)']));
    render(
      <ThemeProvider surface="gallery">
        <Probe />
      </ThemeProvider>
    );
    expect(screen.getByTestId('reduced-motion')).toHaveTextContent('true');
    media.set('(prefers-reduced-motion: reduce)', false);
    expect(screen.getByTestId('reduced-motion')).toHaveTextContent('false');
  });

  it('localStorage 不可用时不影响本次会话', () => {
    stubMatchMedia(new Set());
    const getItem = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('storage disabled');
    });
    const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('storage disabled');
    });
    expect(() =>
      render(
        <ThemeProvider surface="manage">
          <Probe />
        </ThemeProvider>
      )
    ).not.toThrow();
    expect(screen.getByTestId('density')).toHaveTextContent('compact');
    getItem.mockRestore();
    setItem.mockRestore();
  });
});

describe('resolveTheme', () => {
  it('显式主题原样返回，system 才查系统偏好', () => {
    stubMatchMedia(new Set(['(prefers-color-scheme: dark)']));
    expect(resolveTheme('light')).toBe('light');
    expect(resolveTheme('dark')).toBe('dark');
    expect(resolveTheme('high-contrast')).toBe('high-contrast');
    expect(resolveTheme('system')).toBe('dark');
  });
});
