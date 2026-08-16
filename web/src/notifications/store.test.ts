import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  createSeenEventStore,
  loadNotificationPreference,
  NOTIFICATION_PREFERENCE_KEY,
  NOTIFICATION_SEEN_KEY,
  NOTIFICATION_SEEN_LIMIT,
  saveNotificationPreference,
} from './store';
import type { StorageLike } from './store';

function memoryStorage(): StorageLike & { data: Map<string, string> } {
  const data = new Map<string, string>();
  return {
    data,
    getItem: (key) => data.get(key) ?? null,
    setItem: (key, value) => {
      data.set(key, value);
    },
    removeItem: (key) => {
      data.delete(key);
    },
  };
}

beforeEach(() => {
  window.localStorage.clear();
});

describe('notification preference', () => {
  it('defaults to off and round-trips through storage', () => {
    const storage = memoryStorage();
    expect(loadNotificationPreference(storage)).toBe(false);

    saveNotificationPreference(true, storage);
    expect(loadNotificationPreference(storage)).toBe(true);
    expect(storage.data.get(NOTIFICATION_PREFERENCE_KEY)).toBe('true');

    saveNotificationPreference(false, storage);
    expect(loadNotificationPreference(storage)).toBe(false);
  });

  it('uses window.localStorage by default', () => {
    expect(loadNotificationPreference()).toBe(false);
    saveNotificationPreference(true);
    expect(window.localStorage.getItem(NOTIFICATION_PREFERENCE_KEY)).toBe('true');
    expect(loadNotificationPreference()).toBe(true);
  });

  it('tolerates a throwing storage', () => {
    const throwing: StorageLike = {
      getItem: () => {
        throw new Error('denied');
      },
      setItem: () => {
        throw new Error('denied');
      },
      removeItem: vi.fn(),
    };
    expect(loadNotificationPreference(throwing)).toBe(false);
    expect(() => saveNotificationPreference(true, throwing)).not.toThrow();
  });
});

describe('seen event store', () => {
  it('records ids and dedupes repeats', () => {
    const store = createSeenEventStore(memoryStorage());
    expect(store.has(8)).toBe(false);

    store.add(8);
    store.add(8);
    expect(store.has(8)).toBe(true);
    expect(store.has(9)).toBe(false);
  });

  it('persists ids so a fresh store after reload still dedupes', () => {
    const storage = memoryStorage();
    createSeenEventStore(storage).add(8);

    const reloaded = createSeenEventStore(storage);
    expect(reloaded.has(8)).toBe(true);
    expect(JSON.parse(storage.data.get(NOTIFICATION_SEEN_KEY) ?? '[]')).toEqual([8]);
  });

  it('merges writes between two live instances sharing one storage', () => {
    // Two open tabs hold a store each, constructed before either writes.
    const storage = memoryStorage();
    const tabA = createSeenEventStore(storage);
    const tabB = createSeenEventStore(storage);

    tabA.add(8);
    // The second tab observes the write at event time, without a reload.
    expect(tabB.has(8)).toBe(true);

    tabB.add(9);
    expect(tabA.has(9)).toBe(true);
    expect(tabA.has(8)).toBe(true);

    // Neither write clobbers the other: one bounded ledger holds both ids.
    expect(JSON.parse(storage.data.get(NOTIFICATION_SEEN_KEY) ?? '[]')).toEqual([8, 9]);
  });

  it('keeps the shared ledger bounded when both instances write', () => {
    const storage = memoryStorage();
    const tabA = createSeenEventStore(storage);
    const tabB = createSeenEventStore(storage);
    for (let id = 1; id <= NOTIFICATION_SEEN_LIMIT; id += 1) {
      tabA.add(id);
    }
    tabB.add(NOTIFICATION_SEEN_LIMIT + 1);

    const persisted: unknown = JSON.parse(storage.data.get(NOTIFICATION_SEEN_KEY) ?? '[]');
    expect(persisted).toHaveLength(NOTIFICATION_SEEN_LIMIT);
    expect(tabA.has(NOTIFICATION_SEEN_LIMIT + 1)).toBe(true);
    expect(tabA.has(1)).toBe(false);
  });

  it('bounds the ledger to the newest ids', () => {
    const storage = memoryStorage();
    const store = createSeenEventStore(storage);
    for (let id = 1; id <= NOTIFICATION_SEEN_LIMIT + 5; id += 1) {
      store.add(id);
    }

    expect(store.has(1)).toBe(false);
    expect(store.has(NOTIFICATION_SEEN_LIMIT + 5)).toBe(true);
    expect(JSON.parse(storage.data.get(NOTIFICATION_SEEN_KEY) ?? '[]')).toHaveLength(
      NOTIFICATION_SEEN_LIMIT,
    );
  });

  it('ignores corrupt persisted data', () => {
    const storage = memoryStorage();
    storage.data.set(NOTIFICATION_SEEN_KEY, 'not json');
    expect(createSeenEventStore(storage).has(8)).toBe(false);

    storage.data.set(NOTIFICATION_SEEN_KEY, JSON.stringify(['x', 8, null]));
    const store = createSeenEventStore(storage);
    expect(store.has(8)).toBe(true);
    expect(store.has('x' as unknown as number)).toBe(false);
  });

  it('still dedupes in memory when storage throws', () => {
    const throwing: StorageLike = {
      getItem: () => {
        throw new Error('denied');
      },
      setItem: () => {
        throw new Error('denied');
      },
      removeItem: vi.fn(),
    };
    const store = createSeenEventStore(throwing);
    expect(() => store.add(8)).not.toThrow();
    expect(store.has(8)).toBe(true);
  });
});
