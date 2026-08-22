/**
 * The dedupe ledger of stream event ids already considered (notified, or
 * deliberately suppressed because the tab was visible or notifications were
 * off).
 *
 * This stays in localStorage while the opt-in and selector do not: it records
 * what *this device* already showed, which is not policy and does not follow
 * the operator to another client. It is also written on every qualifying
 * event, so a server round trip would land on the hot path of exactly the
 * alert storm where it would hurt. Every access is defensive because storage
 * can be unavailable or throw (private modes, the future Tauri shell), in
 * which case the ledger degrades to in-memory behavior.
 */

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
