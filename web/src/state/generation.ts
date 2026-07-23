export class GenerationGate {
  #generation = 0;

  next(): number {
    this.#generation += 1;
    return this.#generation;
  }

  current(): number {
    return this.#generation;
  }

  accepts(generation: number): boolean {
    return generation === this.#generation;
  }
}

export function acceptSequence(current: number, incoming: number): 'accept' | 'duplicate' | 'gap' {
  if (incoming <= current) return 'duplicate';
  if (current !== 0 && incoming !== current + 1) return 'gap';
  return 'accept';
}
