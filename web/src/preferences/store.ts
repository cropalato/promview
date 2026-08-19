import { DEFAULT_COLUMN_IDS } from '../alerts/columns';

/**
 * Console layout preferences: which columns, how dense, whether alerts arrive
 * grouped.
 *
 * These live on the server so they follow an operator between machines. In open
 * mode there is no user to key them against, and the endpoint says so with a
 * 404; the console then keeps them in localStorage instead. Every storage access
 * is defensive because it can be unavailable or throw (private modes, the future
 * Tauri shell).
 */

export const PREFERENCES_URL = '/api/v1/preferences';
export const PREFERENCES_KEY = 'promview.preferences';

/**
 * `auto` defers the row height to the console, which resolves it from the area
 * the table has; see preferences/density.ts.
 */
export type Density = 'auto' | 'compact' | 'normal' | 'comfortable';

export const DENSITIES: readonly Density[] = ['auto', 'compact', 'normal', 'comfortable'];

export interface ColumnPreference {
  id: string;
  width?: number;
}

export interface GroupingPreference {
  enabled: boolean;
  keys: string[];
}

export interface Preferences {
  columns: ColumnPreference[];
  density: Density;
  grouping: GroupingPreference;
}

export function defaultPreferences(): Preferences {
  return {
    columns: DEFAULT_COLUMN_IDS.map((id) => ({ id })),
    density: 'auto',
    grouping: { enabled: true, keys: ['alertname', 'source'] },
  };
}

/** Where the current preferences came from, which decides where they are saved. */
export type PreferencesOrigin = 'server' | 'local';

export interface LoadedPreferences {
  preferences: Preferences;
  origin: PreferencesOrigin;
}

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

function isDensity(value: unknown): value is Density {
  return typeof value === 'string' && (DENSITIES as readonly string[]).includes(value);
}

/**
 * Parses a stored payload, falling back per field. A layout written by another
 * version should cost the fields it got wrong, not the whole console.
 */
export function parsePreferences(value: unknown): Preferences {
  const defaults = defaultPreferences();
  if (typeof value !== 'object' || value === null) {
    return defaults;
  }
  const raw = value as Record<string, unknown>;
  const columns: ColumnPreference[] = [];
  if (Array.isArray(raw.columns)) {
    for (const entry of raw.columns) {
      if (typeof entry !== 'object' || entry === null) {
        continue;
      }
      const column = entry as Record<string, unknown>;
      if (typeof column.id !== 'string' || column.id === '') {
        continue;
      }
      columns.push(
        typeof column.width === 'number' && Number.isFinite(column.width)
          ? { id: column.id, width: column.width }
          : { id: column.id },
      );
    }
  }
  const grouping = ((): GroupingPreference => {
    if (typeof raw.grouping !== 'object' || raw.grouping === null) {
      return defaults.grouping;
    }
    const value = raw.grouping as Record<string, unknown>;
    const keys = Array.isArray(value.keys)
      ? value.keys.filter((key): key is string => typeof key === 'string')
      : defaults.grouping.keys;
    return {
      enabled: typeof value.enabled === 'boolean' ? value.enabled : defaults.grouping.enabled,
      keys,
    };
  })();

  return {
    columns: columns.length > 0 ? columns : defaults.columns,
    density: isDensity(raw.density) ? raw.density : defaults.density,
    grouping,
  };
}

export function readLocalPreferences(storage?: StorageLike): Preferences {
  const store = storage ?? defaultStorage();
  try {
    const raw = store?.getItem(PREFERENCES_KEY);
    return raw === null || raw === undefined
      ? defaultPreferences()
      : parsePreferences(JSON.parse(raw));
  } catch {
    return defaultPreferences();
  }
}

export function writeLocalPreferences(value: Preferences, storage?: StorageLike): void {
  const store = storage ?? defaultStorage();
  try {
    store?.setItem(PREFERENCES_KEY, JSON.stringify(value));
  } catch {
    // Storage is unavailable; the layout lasts for this session only.
  }
}

/**
 * Loads preferences, preferring the server. A 404 means this deployment has no
 * user to key against, so the browser copy is authoritative instead — that is a
 * statement about identity, not a failure, and the caller needs to know which
 * one it got so it saves back to the same place.
 */
export async function loadPreferences(storage?: StorageLike): Promise<LoadedPreferences> {
  try {
    const response = await fetch(PREFERENCES_URL, {
      headers: { Accept: 'application/json' },
      credentials: 'same-origin',
    });
    if (response.status === 404) {
      return { preferences: readLocalPreferences(storage), origin: 'local' };
    }
    if (!response.ok) {
      throw new Error(`preferences request failed with ${response.status}`);
    }
    return { preferences: parsePreferences(await response.json()), origin: 'server' };
  } catch {
    // The console is usable without its layout; fall back rather than block it.
    return { preferences: readLocalPreferences(storage), origin: 'local' };
  }
}

/** Saves preferences to wherever they were loaded from. */
export async function savePreferences(
  value: Preferences,
  origin: PreferencesOrigin,
  storage?: StorageLike,
): Promise<PreferencesOrigin> {
  // The local copy is written either way: it is what the console reads on the
  // next boot before the server answers, so the table does not flash a default
  // layout and then rearrange.
  writeLocalPreferences(value, storage);
  if (origin === 'local') {
    return 'local';
  }
  try {
    const response = await fetch(PREFERENCES_URL, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify(value),
    });
    if (response.status === 404) {
      return 'local';
    }
    if (!response.ok) {
      throw new Error(`preferences save failed with ${response.status}`);
    }
    return 'server';
  } catch {
    return 'local';
  }
}
