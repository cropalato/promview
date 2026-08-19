import '@testing-library/jest-dom/vitest';

// jsdom does not implement PointerEvent; the tests exercise pointer-driven
// behaviour (column resizing) through a MouseEvent with the pointer fields
// the component reads.
if (typeof window !== 'undefined' && window.PointerEvent === undefined) {
  class PointerEventPolyfill extends MouseEvent {
    public readonly pointerId: number;

    constructor(type: string, init: PointerEventInit = {}) {
      super(type, init);
      this.pointerId = init.pointerId ?? 0;
    }
  }
  window.PointerEvent = PointerEventPolyfill as unknown as typeof PointerEvent;
}

// Pointer capture goes with PointerEvent: jsdom has neither.
if (typeof Element !== 'undefined' && Element.prototype.setPointerCapture === undefined) {
  const captured = new WeakMap<Element, number>();
  Element.prototype.setPointerCapture = function setPointerCapture(pointerId: number): void {
    captured.set(this, pointerId);
  };
  Element.prototype.releasePointerCapture = function releasePointerCapture(): void {
    captured.delete(this);
  };
  Element.prototype.hasPointerCapture = function hasPointerCapture(pointerId: number): boolean {
    return captured.get(this) === pointerId;
  };
}
