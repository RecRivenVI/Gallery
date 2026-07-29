/*
 * 有身份列表的连续布局动效。
 *
 * 这不是一个“所有节点统一入场”的装饰器。调用方提供稳定业务键后，本 hook 只解释三件事：
 *
 * - 同一键还存在：从上一布局位置移动到新位置；
 * - 新键出现：在自己的最终位置做轻量显现；
 * - 旧键消失：留下短暂、不可交互且不进入无障碍树的视觉副本。
 *
 * 数据、焦点和点击目标永远属于当前真实 DOM。动效可被下一次更新打断；若节点仍在上一段
 * 动画中，会先读取它当下的视觉位置，再接到最新布局。超出可见区预算、浏览器不支持 WAAPI
 * 或用户要求减少动效时，列表直接提交最终状态。
 */

import { useCallback, useLayoutEffect, useRef } from 'react';
import type { RefCallback } from 'react';

interface MotionRect {
  top: number;
  left: number;
  right: number;
  bottom: number;
  width: number;
  height: number;
}

interface MotionSnapshot {
  rect: MotionRect;
  ghost: HTMLElement;
}

export interface KeyedLayoutMotionOptions {
  reducedMotion: boolean;
  enabled?: boolean;
  /** 只保留可见区附近有限数量的几何与视觉副本，避免无限列表让动效成本线性增长。 */
  maxAnimatedItems?: number;
}

export interface KeyedLayoutMotion {
  itemRef: (key: string) => RefCallback<HTMLElement>;
}

const DEFAULT_MAX_ANIMATED_ITEMS = 72;
const VIEWPORT_MARGIN_PX = 480;
const FALLBACK_STRUCTURE_MS = 280;
const FALLBACK_STATE_MS = 180;
const FALLBACK_STRUCTURE_EASING = 'cubic-bezier(0.16, 1, 0.3, 1)';
const FALLBACK_EXIT_EASING = 'cubic-bezier(0.4, 0, 1, 1)';

function readDuration(name: string, fallback: number): number {
  if (typeof document === 'undefined') return 0;
  const value = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  if (value.endsWith('ms')) {
    const parsed = Number.parseFloat(value);
    return Number.isFinite(parsed) ? parsed : fallback;
  }
  if (value.endsWith('s')) {
    const parsed = Number.parseFloat(value);
    return Number.isFinite(parsed) ? parsed * 1000 : fallback;
  }
  return fallback;
}

function readEasing(name: string, fallback: string): string {
  if (typeof document === 'undefined') return fallback;
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback;
}

function documentRect(element: HTMLElement): MotionRect {
  const rect = element.getBoundingClientRect();
  const scrollX = typeof window === 'undefined' ? 0 : window.scrollX;
  const scrollY = typeof window === 'undefined' ? 0 : window.scrollY;
  return {
    top: rect.top + scrollY,
    left: rect.left + scrollX,
    right: rect.right + scrollX,
    bottom: rect.bottom + scrollY,
    width: rect.width,
    height: rect.height
  };
}

function nearViewport(rect: MotionRect): boolean {
  if (typeof window === 'undefined') return false;
  const top = rect.top - window.scrollY;
  const bottom = rect.bottom - window.scrollY;
  const left = rect.left - window.scrollX;
  const right = rect.right - window.scrollX;
  return (
    bottom >= -VIEWPORT_MARGIN_PX &&
    top <= window.innerHeight + VIEWPORT_MARGIN_PX &&
    right >= -VIEWPORT_MARGIN_PX &&
    left <= window.innerWidth + VIEWPORT_MARGIN_PX
  );
}

function canAnimate(element: HTMLElement): boolean {
  return typeof element.animate === 'function';
}

function stripGhostSemantics(ghost: HTMLElement) {
  ghost.setAttribute('aria-hidden', 'true');
  ghost.setAttribute('inert', '');
  ghost.removeAttribute('id');
  for (const element of ghost.querySelectorAll<HTMLElement>(
    '[id], a, button, input, select, textarea, [tabindex]'
  )) {
    element.removeAttribute('id');
    element.setAttribute('tabindex', '-1');
  }
}

function removeGhostAfter(animation: Animation, ghost: HTMLElement, duration: number) {
  const remove = () => ghost.remove();
  animation.onfinish = remove;
  animation.oncancel = remove;
  // 浏览器或测试环境可能漏报 finish；视觉副本必须有独立收尾兜底。
  window.setTimeout(remove, Math.max(FALLBACK_STRUCTURE_MS, duration) * 3);
}

function animateGhost(snapshot: MotionSnapshot, duration: number, easing: string) {
  const ghost = snapshot.ghost;
  if (!canAnimate(ghost) || duration <= 0) return;

  stripGhostSemantics(ghost);
  ghost.classList.add('ui-motion-ghost');
  Object.assign(ghost.style, {
    position: 'fixed',
    top: `${snapshot.rect.top - window.scrollY}px`,
    left: `${snapshot.rect.left - window.scrollX}px`,
    width: `${snapshot.rect.width}px`,
    height: `${snapshot.rect.height}px`
  });
  document.body.append(ghost);
  const animation = ghost.animate(
    [
      { opacity: 1, transform: 'translate3d(0, 0, 0) scale(1)' },
      { opacity: 0, transform: 'translate3d(0, 0, 0) scale(0.975)' }
    ],
    { duration, easing, fill: 'both' }
  );
  removeGhostAfter(animation, ghost, duration);
}

/**
 * 为稳定键列表提供最后状态获胜的连续布局反馈。
 *
 * `keys` 的顺序必须与 DOM 顺序一致；`itemRef(key)` 要挂在代表该业务对象的最外层真实节点上。
 */
export function useKeyedLayoutMotion(
  keys: readonly string[],
  { reducedMotion, enabled = true, maxAnimatedItems = DEFAULT_MAX_ANIMATED_ITEMS }: KeyedLayoutMotionOptions
): KeyedLayoutMotion {
  const nodes = useRef(new Map<string, HTMLElement>());
  const callbacks = useRef(new Map<string, RefCallback<HTMLElement>>());
  const previousSnapshots = useRef<Map<string, MotionSnapshot> | null>(null);
  const previousKeys = useRef<Set<string> | null>(null);
  const activeAnimations = useRef(new Map<string, Animation>());
  const signature = JSON.stringify(keys);

  const itemRef = useCallback((key: string): RefCallback<HTMLElement> => {
    const existing = callbacks.current.get(key);
    if (existing !== undefined) return existing;
    const callback: RefCallback<HTMLElement> = (node) => {
      if (node === null) nodes.current.delete(key);
      else nodes.current.set(key, node);
    };
    callbacks.current.set(key, callback);
    return callback;
  }, []);

  useLayoutEffect(() => {
    const currentOrder = JSON.parse(signature) as string[];
    const currentKeys = new Set(currentOrder);
    const oldSnapshots = previousSnapshots.current;
    const oldKeys = previousKeys.current;
    const allowMotion = enabled && !reducedMotion;
    const structureDuration = allowMotion ? readDuration('--motion-structure', FALLBACK_STRUCTURE_MS) : 0;
    const stateDuration = allowMotion ? readDuration('--motion-state', FALLBACK_STATE_MS) : 0;
    const structureEasing = readEasing('--ease-structure', FALLBACK_STRUCTURE_EASING);
    const exitEasing = readEasing('--ease-exit', FALLBACK_EXIT_EASING);
    // matchMedia 的 change 事件可能比 CSS 媒体查询晚一个事件拍到达。计算后的 token 已归零时
    // 同样视为减弱动画，不能创建一批“时长为 0”的 WAAPI 对象。
    const hasMotionBudget = allowMotion && (structureDuration > 0 || stateDuration > 0);

    if (!hasMotionBudget) {
      for (const animation of activeAnimations.current.values()) animation.cancel();
      activeAnimations.current.clear();
    }

    if (hasMotionBudget && oldSnapshots !== null && oldKeys !== null) {
      for (const [key, snapshot] of oldSnapshots) {
        if (!currentKeys.has(key) && nearViewport(snapshot.rect)) {
          activeAnimations.current.get(key)?.cancel();
          activeAnimations.current.delete(key);
          animateGhost(snapshot, stateDuration, exitEasing);
        }
      }

      for (const key of currentOrder) {
        const node = nodes.current.get(key);
        if (node === undefined || !canAnimate(node)) continue;

        const running = activeAnimations.current.get(key);
        let target = documentRect(node);
        let origin = oldSnapshots.get(key)?.rect;
        if (running !== undefined) {
          // DOM 已提交最新布局，但上一段 WAAPI transform 仍代表屏幕上的当前位置。
          origin = documentRect(node);
          running.cancel();
          activeAnimations.current.delete(key);
          target = documentRect(node);
        }
        if (!nearViewport(target)) continue;

        const isNew = !oldKeys.has(key);
        let animation: Animation | undefined;
        if (origin !== undefined) {
          const deltaX = origin.left - target.left;
          const deltaY = origin.top - target.top;
          if (structureDuration > 0 && (Math.abs(deltaX) >= 0.5 || Math.abs(deltaY) >= 0.5)) {
            animation = node.animate(
              [
                { transform: `translate3d(${deltaX}px, ${deltaY}px, 0)` },
                { transform: 'translate3d(0, 0, 0)' }
              ],
              { duration: structureDuration, easing: structureEasing, fill: 'both' }
            );
          }
        } else if (isNew && stateDuration > 0) {
          animation = node.animate(
            [
              { opacity: 0, transform: 'translate3d(0, var(--space-2), 0) scale(0.985)' },
              { opacity: 1, transform: 'translate3d(0, 0, 0) scale(1)' }
            ],
            { duration: stateDuration, easing: structureEasing, fill: 'both' }
          );
        }

        if (animation !== undefined) {
          activeAnimations.current.set(key, animation);
          animation.onfinish = () => {
            if (activeAnimations.current.get(key) !== animation) return;
            activeAnimations.current.delete(key);
            // fill:both 只用于覆盖首帧和结束交界；完成后必须清掉 effect，不能让已结束
            // Animation 长期挂在节点上并参与后续 getAnimations/合成资源。
            animation.cancel();
          };
          animation.oncancel = () => {
            if (activeAnimations.current.get(key) === animation) activeAnimations.current.delete(key);
          };
        }
      }
    }

    const nextSnapshots = new Map<string, MotionSnapshot>();
    let measured = 0;
    for (const key of currentOrder) {
      if (measured >= maxAnimatedItems) break;
      const node = nodes.current.get(key);
      if (node === undefined) continue;
      const rect = documentRect(node);
      if (!nearViewport(rect)) continue;
      nextSnapshots.set(key, { rect, ghost: node.cloneNode(true) as HTMLElement });
      measured += 1;
    }
    previousSnapshots.current = nextSnapshots;
    previousKeys.current = currentKeys;
    for (const key of callbacks.current.keys()) {
      if (!currentKeys.has(key)) callbacks.current.delete(key);
    }
  }, [enabled, maxAnimatedItems, reducedMotion, signature]);

  useLayoutEffect(
    () => () => {
      for (const animation of activeAnimations.current.values()) animation.cancel();
      activeAnimations.current.clear();
    },
    []
  );

  return { itemRef };
}
