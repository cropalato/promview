import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';

import { useResolvedDensity } from './useResolvedDensity';

function setViewportHeight(height: number): void {
  Object.defineProperty(window, 'innerHeight', {
    value: height,
    configurable: true,
    writable: true,
  });
}

const originalHeight = window.innerHeight;

afterEach(() => {
  setViewportHeight(originalHeight);
});

describe('useResolvedDensity', () => {
  it('resolves auto from the current viewport', () => {
    setViewportHeight(600);
    const { result } = renderHook(() => useResolvedDensity('auto'));
    expect(result.current).toBe('compact');
  });

  it('re-resolves when the window changes, so moving between screens lands right', () => {
    setViewportHeight(900);
    const { result } = renderHook(() => useResolvedDensity('auto'));
    expect(result.current).toBe('normal');

    act(() => {
      setViewportHeight(1400);
      window.dispatchEvent(new Event('resize'));
    });
    expect(result.current).toBe('comfortable');
  });

  it('does not measure when the operator chose a density', () => {
    setViewportHeight(400);
    const { result } = renderHook(() => useResolvedDensity('comfortable'));
    expect(result.current).toBe('comfortable');
  });
});
