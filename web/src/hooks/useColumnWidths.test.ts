import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import { COLUMN_WIDTHS_KEY, MAX_COLUMN_WIDTH } from '../preferences/columnWidths';
import { useColumnWidths } from './useColumnWidths';

beforeEach(() => {
  window.localStorage.clear();
});

describe('useColumnWidths', () => {
  it('restores stored widths on first render', () => {
    window.localStorage.setItem(COLUMN_WIDTHS_KEY, JSON.stringify({ summary: 300 }));
    const { result } = renderHook(() => useColumnWidths());
    expect(result.current.widths).toEqual({ summary: 300 });
  });

  it('ignores a malformed stored payload', () => {
    window.localStorage.setItem(COLUMN_WIDTHS_KEY, 'not json');
    const { result } = renderHook(() => useColumnWidths());
    expect(result.current.widths).toEqual({});
  });

  it('clamps and persists a resize', () => {
    const { result } = renderHook(() => useColumnWidths());
    act(() => result.current.setColumnWidth('summary', 5000));
    expect(result.current.widths).toEqual({ summary: MAX_COLUMN_WIDTH });
    expect(JSON.parse(window.localStorage.getItem(COLUMN_WIDTHS_KEY) ?? '{}')).toEqual({
      summary: MAX_COLUMN_WIDTH,
    });
  });

  it('drops a width on reset', () => {
    const { result } = renderHook(() => useColumnWidths());
    act(() => result.current.setColumnWidth('summary', 300));
    act(() => result.current.resetColumnWidth('summary'));
    expect(result.current.widths).toEqual({});
    expect(JSON.parse(window.localStorage.getItem(COLUMN_WIDTHS_KEY) ?? '{}')).toEqual({});
  });
});
