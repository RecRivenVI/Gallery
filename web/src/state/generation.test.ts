import { describe, expect, it } from 'vitest';
import { GenerationGate, acceptSequence } from './generation';

describe('GenerationGate', () => {
  it('拒绝已经过时的异步响应', () => {
    const gate = new GenerationGate();
    const oldRequest = gate.next();
    const currentRequest = gate.next();
    expect(gate.accepts(oldRequest)).toBe(false);
    expect(gate.accepts(currentRequest)).toBe(true);
  });
});

describe('acceptSequence', () => {
  it.each([
    [0, 1, 'accept'],
    [3, 3, 'duplicate'],
    [3, 2, 'duplicate'],
    [3, 5, 'gap']
  ] as const)('从 %i 接收 %i 时返回 %s', (current, incoming, expected) => {
    expect(acceptSequence(current, incoming)).toBe(expected);
  });
});
