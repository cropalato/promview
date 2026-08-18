import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  PREFERENCES_KEY,
  defaultPreferences,
  loadPreferences,
  parsePreferences,
  readLocalPreferences,
  savePreferences,
} from './store';

function fetchMock(): ReturnType<typeof vi.fn> {
  return globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn());
  window.localStorage.clear();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('preferences store', () => {
  it('loads from the server when a user is signed in', async () => {
    fetchMock().mockResolvedValue(
      jsonResponse({
        columns: [{ id: 'severity' }, { id: 'label:prometheus_cluster', width: 180 }],
        density: 'compact',
        grouping: { enabled: false, keys: ['alertname'] },
      }),
    );

    const loaded = await loadPreferences();

    expect(loaded.origin).toBe('server');
    expect(loaded.preferences.density).toBe('compact');
    expect(loaded.preferences.columns).toHaveLength(2);
    expect(loaded.preferences.columns[1]).toEqual({ id: 'label:prometheus_cluster', width: 180 });
    expect(loaded.preferences.grouping.enabled).toBe(false);
  });

  it('falls back to the browser when the deployment has no user to key against', async () => {
    // Open mode answers 404: there is no user, which is not a failure.
    fetchMock().mockResolvedValue(new Response('{}', { status: 404 }));
    window.localStorage.setItem(
      PREFERENCES_KEY,
      JSON.stringify({ ...defaultPreferences(), density: 'comfortable' }),
    );

    const loaded = await loadPreferences();

    expect(loaded.origin).toBe('local');
    expect(loaded.preferences.density).toBe('comfortable');
  });

  it('falls back to the browser when the request fails', async () => {
    fetchMock().mockRejectedValue(new TypeError('fetch failed'));
    const loaded = await loadPreferences();
    // The console is usable without its layout; a failed load must not block it.
    expect(loaded.origin).toBe('local');
    expect(loaded.preferences).toEqual(defaultPreferences());
  });

  it('saves to the server and mirrors locally so the next boot does not flash', async () => {
    fetchMock().mockResolvedValue(jsonResponse({}));
    const value = { ...defaultPreferences(), density: 'compact' as const };

    const origin = await savePreferences(value, 'server');

    expect(origin).toBe('server');
    const [url, init] = fetchMock().mock.calls[0] as [string, RequestInit];
    expect(url).toBe('/api/v1/preferences');
    expect(init.method).toBe('PUT');
    expect(JSON.parse(String(init.body)).density).toBe('compact');
    expect(readLocalPreferences().density).toBe('compact');
  });

  it('does not call the server when preferences are browser-local', async () => {
    const origin = await savePreferences({ ...defaultPreferences(), density: 'compact' }, 'local');
    expect(origin).toBe('local');
    expect(fetchMock()).not.toHaveBeenCalled();
    expect(readLocalPreferences().density).toBe('compact');
  });

  it('downgrades to local when the server rejects the save', async () => {
    fetchMock().mockResolvedValue(new Response('{}', { status: 404 }));
    const origin = await savePreferences(defaultPreferences(), 'server');
    expect(origin).toBe('local');
  });

  it('fills in fields a stored payload got wrong instead of discarding it', () => {
    // A layout written by another version should cost the fields it got wrong,
    // not the whole console.
    const parsed = parsePreferences({
      columns: [{ id: 'severity' }, 'nonsense', { width: 12 }],
      density: 'enormous',
      grouping: { enabled: 'yes' },
    });

    expect(parsed.columns).toEqual([{ id: 'severity' }]);
    expect(parsed.density).toBe('normal');
    expect(parsed.grouping.enabled).toBe(true);
    expect(parsed.grouping.keys).toEqual(['alertname', 'source']);
  });

  it('survives unreadable storage', () => {
    const throwing = {
      getItem() {
        throw new Error('denied');
      },
      setItem() {
        throw new Error('denied');
      },
    };
    expect(readLocalPreferences(throwing)).toEqual(defaultPreferences());
  });
});
