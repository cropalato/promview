import { DEFAULT_COLUMN_IDS } from '../alerts/columns';
import { DEFAULT_GROUP_KEYS, sanitizeGroupKeys } from '../alerts/grouping';
import { isTheme } from './theme';
import type { Theme } from './theme';
import { apiUrl } from '../config/apiBase';

/**
 * Console layout preferences: which columns, how dense, whether alerts arrive
 * grouped, and which palette the console renders in.
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

/**
 * A notification selector matches on the fields a stream event carries, which
 * is not every alert label: the server denormalized a handful into the stream
 * record, and a selector naming anything else could never fire. The server
 * refuses those at write time rather than letting them fail silently.
 */
export const NOTIFICATION_FIELDS: readonly string[] = ['severity', 'alertname', 'source', 'team'];

export interface NotificationMatcher {
  name: string;
  op: '=' | '!=';
  value: string;
}

export interface NotificationPreference {
  enabled: boolean;
  /** ANDed. Empty matches nothing, never everything. */
  matchers: NotificationMatcher[];
}

export interface Preferences {
  columns: ColumnPreference[];
  density: Density;
  grouping: GroupingPreference;
  theme: Theme;
  notifications: NotificationPreference;
}

export function defaultPreferences(): Preferences {
  return {
    columns: DEFAULT_COLUMN_IDS.map((id) => ({ id })),
    density: 'auto',
    grouping: { enabled: true, keys: [...DEFAULT_GROUP_KEYS] },
    theme: 'system',
    // Off, carrying the selector the console hardcoded before this was
    // configurable, so opting in does what it always did.
    notifications: { enabled: false, matchers: [{ name: 'severity', op: '=', value: 'critical' }] },
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
    // Keys outside the API's grouping vocabulary (or more than it accepts)
    // would make every grouped request fail; a layout naming them keeps the
    // usable prefix instead of costing the whole grouping preference.
    const keys = Array.isArray(value.keys)
      ? sanitizeGroupKeys(value.keys, defaults.grouping.keys)
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
    theme: isTheme(raw.theme) ? raw.theme : defaults.theme,
    notifications: parseNotifications(raw.notifications, defaults.notifications),
  };
}

/**
 * A selector written by another version, or naming a field this console cannot
 * evaluate, costs the matchers it got wrong rather than the whole preference.
 * A payload with no notifications key at all predates the feature and takes the
 * default.
 */
function parseNotifications(
  value: unknown,
  fallback: NotificationPreference,
): NotificationPreference {
  if (typeof value !== 'object' || value === null) {
    return fallback;
  }
  const raw = value as Record<string, unknown>;
  if (!Array.isArray(raw.matchers)) {
    return { enabled: raw.enabled === true, matchers: fallback.matchers };
  }
  const matchers: NotificationMatcher[] = [];
  for (const entry of raw.matchers) {
    if (typeof entry !== 'object' || entry === null) {
      continue;
    }
    const matcher = entry as Record<string, unknown>;
    if (typeof matcher.name !== 'string' || !NOTIFICATION_FIELDS.includes(matcher.name)) {
      continue;
    }
    if (matcher.op !== '=' && matcher.op !== '!=') {
      continue;
    }
    if (typeof matcher.value !== 'string' || matcher.value === '') {
      continue;
    }
    matchers.push({ name: matcher.name, op: matcher.op, value: matcher.value });
  }
  return { enabled: raw.enabled === true, matchers };
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
    const response = await fetch(apiUrl(PREFERENCES_URL), {
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
    const response = await fetch(apiUrl(PREFERENCES_URL), {
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
