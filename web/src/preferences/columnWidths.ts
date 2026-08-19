/**
 * Resized alert-table column widths, keyed by column id.
 *
 * Unlike the layout preferences these stay in the browser: a width is a
 * property of the screen it was dragged on, so following an operator from a
 * wall display to a laptop would be wrong on both. Every access is defensive
 * because storage can be unavailable, throw, or hold a payload written by
 * another version — a malformed entry costs that one width, not the table.
 */

export const COLUMN_WIDTHS_KEY = 'promview.columnWidths';

/** Below this a header label stops fitting; above it one column eats the panel. */
export const MIN_COLUMN_WIDTH = 64;
export const MAX_COLUMN_WIDTH = 640;

export type ColumnWidths = Record<string, number>;

interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

function defaultStorage(): StorageLike | undefined {
  try {
    return typeof window === 'undefined' ? undefined : window.localStorage;
  } catch {
    return undefined;
  }
}

export function clampColumnWidth(width: number): number {
  return Math.min(Math.max(Math.round(width), MIN_COLUMN_WIDTH), MAX_COLUMN_WIDTH);
}

/**
 * Parses a stored payload, dropping entries that are not finite numbers and
 * clamping the rest into the supported range.
 */
export function parseColumnWidths(value: unknown): ColumnWidths {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return {};
  }
  const widths: ColumnWidths = {};
  for (const [id, width] of Object.entries(value)) {
    if (id === '' || typeof width !== 'number' || !Number.isFinite(width)) {
      continue;
    }
    widths[id] = clampColumnWidth(width);
  }
  return widths;
}

export function readColumnWidths(storage?: StorageLike): ColumnWidths {
  const store = storage ?? defaultStorage();
  try {
    const raw = store?.getItem(COLUMN_WIDTHS_KEY);
    return raw === null || raw === undefined ? {} : parseColumnWidths(JSON.parse(raw));
  } catch {
    return {};
  }
}

export function writeColumnWidths(widths: ColumnWidths, storage?: StorageLike): void {
  const store = storage ?? defaultStorage();
  try {
    store?.setItem(COLUMN_WIDTHS_KEY, JSON.stringify(widths));
  } catch {
    // Storage is unavailable; the widths last for this session only.
  }
}
