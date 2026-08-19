import { beforeEach, describe, expect, it } from 'vitest';
import {
  COLUMN_WIDTHS_KEY,
  MAX_COLUMN_WIDTH,
  MIN_COLUMN_WIDTH,
  clampColumnWidth,
  parseColumnWidths,
  readColumnWidths,
  writeColumnWidths,
} from './columnWidths';

beforeEach(() => {
  window.localStorage.clear();
});

describe('clampColumnWidth', () => {
  it('keeps widths inside the supported range', () => {
    expect(clampColumnWidth(10)).toBe(MIN_COLUMN_WIDTH);
    expect(clampColumnWidth(2000)).toBe(MAX_COLUMN_WIDTH);
    expect(clampColumnWidth(180.4)).toBe(180);
  });
});

describe('parseColumnWidths', () => {
  it('keeps valid entries', () => {
    expect(parseColumnWidths({ severity: 120, summary: 320 })).toEqual({
      severity: 120,
      summary: 320,
    });
  });

  it('clamps out-of-range entries instead of trusting them', () => {
    expect(parseColumnWidths({ severity: 1, summary: 99999 })).toEqual({
      severity: MIN_COLUMN_WIDTH,
      summary: MAX_COLUMN_WIDTH,
    });
  });

  it('drops malformed entries and keeps the rest', () => {
    expect(
      parseColumnWidths({ severity: 120, summary: 'wide', team: Number.NaN, '': 100 }),
    ).toEqual({ severity: 120 });
  });

  it('treats non-object payloads as empty', () => {
    expect(parseColumnWidths(null)).toEqual({});
    expect(parseColumnWidths('wide')).toEqual({});
    expect(parseColumnWidths([120])).toEqual({});
    expect(parseColumnWidths(42)).toEqual({});
  });
});

describe('readColumnWidths', () => {
  it('returns empty when nothing is stored', () => {
    expect(readColumnWidths()).toEqual({});
  });

  it('returns empty when the stored payload is not JSON', () => {
    window.localStorage.setItem(COLUMN_WIDTHS_KEY, '{oops');
    expect(readColumnWidths()).toEqual({});
  });

  it('returns empty when storage throws', () => {
    const broken = {
      getItem: () => {
        throw new Error('denied');
      },
      setItem: () => undefined,
    };
    expect(readColumnWidths(broken)).toEqual({});
  });

  it('round-trips through storage', () => {
    writeColumnWidths({ severity: 140 });
    expect(readColumnWidths()).toEqual({ severity: 140 });
  });
});

describe('writeColumnWidths', () => {
  it('does not throw when storage is unavailable', () => {
    const broken = {
      getItem: () => null,
      setItem: () => {
        throw new Error('denied');
      },
    };
    expect(() => writeColumnWidths({ severity: 140 }, broken)).not.toThrow();
  });
});
