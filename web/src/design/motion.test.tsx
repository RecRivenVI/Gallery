import { cleanup, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useKeyedLayoutMotion } from './motion';

interface AnimationRecord {
  element: HTMLElement;
  keyframes: Keyframe[] | PropertyIndexedKeyframes | null;
  options: number | KeyframeAnimationOptions | undefined;
  animation: Animation;
  cancelState: { count: number };
}

const records: AnimationRecord[] = [];

function rect(left: number): DOMRect {
  return {
    x: left,
    y: 0,
    top: 0,
    left,
    right: left + 80,
    bottom: 120,
    width: 80,
    height: 120,
    toJSON: () => ({})
  } as DOMRect;
}

function Harness({
  items,
  positions,
  reducedMotion = false
}: {
  items: readonly string[];
  positions: Record<string, number>;
  reducedMotion?: boolean;
}) {
  const motion = useKeyedLayoutMotion(items, { reducedMotion });
  return (
    <div>
      {items.map((item) => (
        <div
          key={item}
          ref={motion.itemRef(item)}
          className="motion-item"
          data-key={item}
          data-left={positions[item]}
        >
          {item}
        </div>
      ))}
    </div>
  );
}

beforeEach(() => {
  records.length = 0;
  document.documentElement.style.setProperty('--motion-state', '180ms');
  document.documentElement.style.setProperty('--motion-structure', '280ms');
  document.documentElement.style.setProperty('--ease-structure', 'cubic-bezier(0.16, 1, 0.3, 1)');
  document.documentElement.style.setProperty('--ease-exit', 'cubic-bezier(0.4, 0, 1, 1)');

  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    const value = Number.parseFloat(this.getAttribute('data-left') ?? this.style.left);
    return rect(Number.isFinite(value) ? value : 0);
  });
  Object.defineProperty(HTMLElement.prototype, 'animate', {
    configurable: true,
    value: vi.fn(function (
      this: HTMLElement,
      keyframes: Keyframe[] | PropertyIndexedKeyframes | null,
      options?: number | KeyframeAnimationOptions
    ) {
      const cancelState = { count: 0 };
      const animation = {
        onfinish: null,
        oncancel: null,
        cancel: vi.fn(function (this: Animation) {
          cancelState.count += 1;
          this.oncancel?.(new Event('cancel') as AnimationPlaybackEvent);
        })
      } as unknown as Animation;
      records.push({ element: this, keyframes, options, animation, cancelState });
      return animation;
    })
  });
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  document.querySelectorAll('.ui-motion-ghost').forEach((element) => element.remove());
  document.documentElement.removeAttribute('style');
});

describe('useKeyedLayoutMotion', () => {
  it('首屏不统一入场，同一业务键重排时从旧位置承接', () => {
    const view = render(<Harness items={['a', 'b']} positions={{ a: 0, b: 100 }} />);
    expect(records).toHaveLength(0);

    view.rerender(<Harness items={['b', 'a']} positions={{ a: 100, b: 0 }} />);

    expect(records).toHaveLength(2);
    expect(records.map((record) => record.element.dataset.key).sort()).toEqual(['a', 'b']);
    expect(JSON.stringify(records[0]?.keyframes)).toContain('translate3d');
    records[0]?.animation.onfinish?.(new Event('finish') as AnimationPlaybackEvent);
    expect(records[0]?.cancelState.count).toBe(1);
  });

  it('只让真正新增的对象在最终位置轻量显现', () => {
    const view = render(<Harness items={['a']} positions={{ a: 0 }} />);
    view.rerender(<Harness items={['a', 'b']} positions={{ a: 0, b: 100 }} />);

    expect(records).toHaveLength(1);
    expect(records[0]?.element.dataset.key).toBe('b');
    expect(JSON.stringify(records[0]?.keyframes)).toContain('0.985');
  });

  it('离场副本不可交互、不进入无障碍树，并在结束后清理', () => {
    const view = render(<Harness items={['a', 'b']} positions={{ a: 0, b: 100 }} />);
    view.rerender(<Harness items={['a']} positions={{ a: 0 }} />);

    const ghost = document.querySelector<HTMLElement>('.ui-motion-ghost');
    expect(ghost).not.toBeNull();
    expect(ghost).toHaveAttribute('aria-hidden', 'true');
    expect(ghost).toHaveAttribute('inert');
    const ghostRecord = records.find((record) => record.element === ghost);
    expect(ghostRecord).toBeDefined();
    ghostRecord?.animation.onfinish?.(new Event('finish') as AnimationPlaybackEvent);
    expect(document.querySelector('.ui-motion-ghost')).toBeNull();
  });

  it('减少动效时直接提交最终布局且不创建离场副本', () => {
    const view = render(<Harness items={['a', 'b']} positions={{ a: 0, b: 100 }} reducedMotion />);
    view.rerender(<Harness items={['b']} positions={{ b: 0 }} reducedMotion />);

    expect(records).toHaveLength(0);
    expect(document.querySelector('.ui-motion-ghost')).toBeNull();
  });
});
