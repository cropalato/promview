/**
 * Persistence for the browser-notification feature: the user's opt-in
 * preference and the dedupe ledger of stream event ids that were already
 * considered (notified or deliberately suppressed). Both live in
 * localStorage so they survive reloads; every access is defensive because
 * storage can be unavailable or throw (private modes, the future Tauri
 * shell), in which case the seen ledger degrades to in-memory behavior.
 */

export const NOTIFICATION_PREFERENCE_KEY = 'promview.notifications.enabled';
export const NOTIFICATION_SEEN_KEY = 'promview.notifications.seenEvents';

/**
 * Upper bound on remembered stream event ids. Alert flow is bursty, so the
 * ledger keeps only the newest ids; anything older has long scrolled past
 * the point where a replayed notification would make sense.
 */
export const NOTIFICATION_SEEN_LIMIT = 200;

/** Structural subset of `Storage` this module needs; `localStorage` fits. */
export interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

function defaultStorage(): StorageLike | undefined {
  try {
    return typeof window === 'undefined' ? undefined : window.localStorage;
  } catch {
    return undefined;
  }
}

/** Persisted opt-in preference; defaults to off and never grants by itself. */
export function loadNotificationPreference(storage?: StorageLike): boolean {
  const store = storage ?? defaultStorage();
  try {
    return store?.getItem(NOTIFICATION_PREFERENCE_KEY) === 'true';
  } catch {
    return false;
  }
}

export function saveNotificationPreference(enabled: boolean, storage?: StorageLike): void {
  const store = storage ?? defaultStorage();
  try {
    store?.setItem(NOTIFICATION_PREFERENCE_KEY, enabled ? 'true' : 'false');
  } catch {
    // Storage unavailable: the preference simply resets on next load.
  }
}

/**
 * Dedupe ledger for stream event ids. Replays happen when the stream
 * resumes across a reload or when Last-Event-ID was lost; any event id in
 * the ledger is never notified again — including events that were
 * suppressed because the document was visible or notifications were off.
 * Every operation merges the persisted ledger at event time, so tabs that
 * are already open observe each other's writes instead of only the state
 * cached at construction.
 */
export interface SeenEventStore {
  has(id: number): boolean;
  add(id: number): void;
}

/** Reads the persisted ledger; null signals that storage itself failed. */
function tryReadSeenIds(store: StorageLike | undefined): number[] | null {
  try {
    const raw = store?.getItem(NOTIFICATION_SEEN_KEY);
    if (raw === null || raw === undefined) {
      return [];
    }
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) {
      return [];
    }
    return parsed.filter((id): id is number => typeof id === 'number' && Number.isFinite(id));
  } catch {
    return null;
  }
}

export function createSeenEventStore(storage?: StorageLike): SeenEventStore {
  const store = storage ?? defaultStorage();
  // Working copy for when storage is unavailable; while storage is healthy
  // the persisted ledger is authoritative and this simply mirrors it.
  let memoryIds = tryReadSeenIds(store) ?? [];

  const persist = (ids: number[]): void => {
    try {
      store?.setItem(NOTIFICATION_SEEN_KEY, JSON.stringify(ids));
    } catch {
      // Storage unavailable: the in-memory list still dedupes this session.
    }
  };

  // Re-read the ledger at event time so already-open tabs observe each
  // other's writes instead of only the state cached at construction.
  const loadLedger = (): number[] => {
    const persisted = tryReadSeenIds(store);
    if (persisted === null) {
      return memoryIds;
    }
    memoryIds = persisted;
    return persisted;
  };

  return {
    has(id) {
      return loadLedger().includes(id);
    },
    add(id) {
      const current = loadLedger();
      if (current.includes(id)) {
        return;
      }
      // Stream event ids are ordered and durable, so appending keeps the
      // newest ids at the tail and the bound trims the oldest first.
      let next = [...current, id];
      if (next.length > NOTIFICATION_SEEN_LIMIT) {
        next = next.slice(next.length - NOTIFICATION_SEEN_LIMIT);
      }
      memoryIds = next;
      persist(next);
    },
  };
}
