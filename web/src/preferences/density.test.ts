import { describe, expect, it } from 'vitest';
import { COMFORTABLE_ABOVE_PX, COMPACT_BELOW_PX, resolveDensity } from './density';

describe('resolveDensity', () => {
  it('honours an explicit choice on every screen', () => {
    // An operator who picked compact keeps compact, however tall the display.
    expect(resolveDensity('compact', 2000)).toBe('compact');
    expect(resolveDensity('comfortable', 400)).toBe('comfortable');
    expect(resolveDensity('normal', 400)).toBe('normal');
  });

  it('tightens rows on a short viewport and relaxes them on a tall one', () => {
    expect(resolveDensity('auto', COMPACT_BELOW_PX - 1)).toBe('compact');
    expect(resolveDensity('auto', COMPACT_BELOW_PX)).toBe('normal');
    expect(resolveDensity('auto', COMFORTABLE_ABOVE_PX - 1)).toBe('normal');
    expect(resolveDensity('auto', COMFORTABLE_ABOVE_PX)).toBe('comfortable');
  });

  it('falls back to normal before anything has been measured', () => {
    // First paint and headless environments report nothing useful; the real
    // answer replaces this as soon as a measurement exists.
    expect(resolveDensity('auto', 0)).toBe('normal');
    expect(resolveDensity('auto', Number.NaN)).toBe('normal');
    expect(resolveDensity('auto', -100)).toBe('normal');
  });
});
