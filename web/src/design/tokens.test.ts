/*
 * 设计 token 的结构断言。
 *
 * 直接读 tokens.css 源文本而不是 getComputedStyle：jsdom 不解析 CSS 自定义属性的层叠，
 * 用它断言主题只会得到「什么都没生效」的假绿。这里断言的是**契约结构**——token 名齐全、
 * 三套显式主题都完整覆盖、显式选择排在系统偏好之后、减少动效把动效 token 归零。
 * 视觉与真实层叠由浏览器 E2E 负责。
 */

import { describe, expect, it } from 'vitest';
import primitives from './primitives.css?raw';
import reset from './reset.css?raw';
import tokens from './tokens.css?raw';

/** 并行工作线依赖的 token 名。改动这个列表等于改动跨线契约。 */
const REQUIRED_TOKENS = [
  '--color-bg',
  '--color-surface-sunken',
  '--color-surface',
  '--color-surface-raised',
  '--color-surface-immersive',
  '--color-border',
  '--color-text',
  '--color-text-muted',
  '--color-accent',
  '--color-accent-text',
  '--color-danger',
  '--color-warning',
  '--color-success',
  '--space-1',
  '--space-2',
  '--space-3',
  '--space-4',
  '--space-5',
  '--space-6',
  '--space-7',
  '--space-8',
  '--radius-sm',
  '--radius-md',
  '--radius-lg',
  '--radius-full',
  '--font-sans',
  '--font-mono',
  '--text-xs',
  '--text-sm',
  '--text-base',
  '--text-lg',
  '--text-xl',
  '--text-2xl',
  '--text-3xl',
  '--leading-tight',
  '--leading-normal',
  '--shadow-sm',
  '--shadow-md',
  '--shadow-lg',
  '--motion-direct',
  '--motion-feedback',
  '--motion-state',
  '--motion-structure',
  '--ease-feedback',
  '--ease-state',
  '--ease-structure',
  '--ease-exit',
  '--motion-fast',
  '--motion-base',
  '--motion-slow',
  '--focus-ring',
  '--control-height',
  '--row-height'
];

/** 每套主题都必须重新声明的颜色 token：漏一个就会从上一层继承出错误配色。 */
const THEME_COLOR_TOKENS = REQUIRED_TOKENS.filter((name) => name.startsWith('--color-'));

function blockOf(selector: string): string {
  const start = tokens.indexOf(selector);
  expect(start, `tokens.css 缺少选择器 ${selector}`).toBeGreaterThanOrEqual(0);
  const open = tokens.indexOf('{', start);
  const end = tokens.indexOf('}', open);
  return tokens.slice(open, end);
}

describe('tokens.css', () => {
  it('在 :root 定义全部契约 token', () => {
    const root = blockOf(':root {');
    for (const name of REQUIRED_TOKENS) {
      expect(root, `:root 缺少 ${name}`).toContain(`${name}:`);
    }
  });

  it('三套显式主题各自完整覆盖颜色 token', () => {
    for (const theme of ['light', 'dark', 'high-contrast']) {
      const block = blockOf(`:root[data-theme='${theme}']`);
      for (const name of THEME_COLOR_TOKENS) {
        expect(block, `data-theme=${theme} 缺少 ${name}`).toContain(`${name}:`);
      }
    }
  });

  it('系统深色偏好只作用于没有显式主题的根元素', () => {
    // 写成 :root:not([data-theme]) 而不是裸 :root 是关键：否则用户显式选择浅色时，
    // 系统深色偏好仍会覆盖它。
    expect(tokens).toContain('@media (prefers-color-scheme: dark)');
    expect(tokens).toContain(':root:not([data-theme])');
  });

  it('显式主题块排在系统偏好媒体查询之后，保证用户选择优先', () => {
    const media = tokens.indexOf('@media (prefers-color-scheme: dark)');
    for (const theme of ['light', 'dark', 'high-contrast']) {
      expect(tokens.indexOf(`:root[data-theme='${theme}']`)).toBeGreaterThan(media);
    }
  });

  it('两种密度都定义控件与行高', () => {
    for (const density of ['comfortable', 'compact']) {
      const block = blockOf(`:root[data-density='${density}']`);
      expect(block).toContain('--control-height:');
      expect(block).toContain('--row-height:');
    }
  });

  it('触控设备把紧凑密度抬回 44px 触控目标', () => {
    const start = tokens.indexOf('@media (pointer: coarse)');
    expect(start).toBeGreaterThanOrEqual(0);
    const block = tokens.slice(start, tokens.indexOf('@media (prefers-reduced-motion', start));
    expect(block).toContain("[data-density='compact']");
    expect(block).toContain('--control-height: 2.75rem');
  });

  it('减少动效时全部时长 token 归零', () => {
    const start = tokens.indexOf('@media (prefers-reduced-motion: reduce)');
    expect(start).toBeGreaterThanOrEqual(0);
    const block = tokens.slice(start);
    for (const name of [
      '--motion-direct',
      '--motion-feedback',
      '--motion-state',
      '--motion-structure',
      '--motion-fast',
      '--motion-base',
      '--motion-slow'
    ]) {
      expect(block).toContain(`${name}: 0ms`);
    }
  });
});

describe('reset.css', () => {
  it('用 :focus-visible 建立可见焦点，且不留下裸 outline:none', () => {
    expect(reset).toContain(':focus-visible');
    expect(reset).toContain('box-shadow: var(--focus-ring)');
  });

  it('减少动效有 !important 兜底，拦住第三方写死的时长', () => {
    // token 归零只能约束我们自己写的样式；react-aria-components 等第三方样式里写死的
    // 动画时长不受 token 控制，必须另有一道全局兜底。
    const start = reset.indexOf('@media (prefers-reduced-motion: reduce)');
    expect(start).toBeGreaterThanOrEqual(0);
    const block = reset.slice(start);
    expect(block).toContain('animation-duration: 0.01ms !important');
    expect(block).toContain('transition-duration: 0.01ms !important');
  });
});

describe('primitives.css', () => {
  it('只用 token，不出现字面颜色', () => {
    // 字面色值会绕开主题层叠：在深色或高对比主题下必然出现读不清的组合。
    const literals = primitives.match(/#[0-9a-fA-F]{3,8}\b|\brgb\(|\bhsl\(/g) ?? [];
    expect(literals, `primitives.css 出现字面颜色：${literals.join(', ')}`).toEqual([]);
  });

  it('减少动效时 Spinner 停止旋转', () => {
    const start = primitives.indexOf('@media (prefers-reduced-motion: reduce)');
    expect(start).toBeGreaterThanOrEqual(0);
    expect(primitives.slice(start)).toContain('animation: none');
  });

  it('图标按钮与控件高度绑定，紧凑密度下不塌成小方块', () => {
    expect(primitives).toContain('width: var(--control-height)');
    expect(primitives).toContain('min-width: var(--control-height)');
  });
});
