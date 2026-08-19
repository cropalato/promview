import { useCallback, useState } from 'react';
import { clampColumnWidth, readColumnWidths, writeColumnWidths } from '../preferences/columnWidths';
import type { ColumnWidths } from '../preferences/columnWidths';

/**
 * Holds the operator's resized column widths for the alert tables.
 *
 * The browser copy is read synchronously on first render so a resized table
 * never flashes its default widths; every change is written straight back.
 * Both the flat and the grouped table read from this one map, keyed by column
 * id, so a column keeps its width whichever view is showing it.
 */
export function useColumnWidths(): {
  widths: Readonly<ColumnWidths>;
  setColumnWidth: (columnId: string, width: number) => void;
  resetColumnWidth: (columnId: string) => void;
} {
  const [widths, setWidths] = useState<ColumnWidths>(() => readColumnWidths());

  const setColumnWidth = useCallback((columnId: string, width: number) => {
    setWidths((current) => {
      const next = { ...current, [columnId]: clampColumnWidth(width) };
      writeColumnWidths(next);
      return next;
    });
  }, []);

  const resetColumnWidth = useCallback((columnId: string) => {
    setWidths((current) => {
      if (!(columnId in current)) {
        return current;
      }
      const next = { ...current };
      delete next[columnId];
      writeColumnWidths(next);
      return next;
    });
  }, []);

  return { widths, setColumnWidth, resetColumnWidth };
}
