import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useAlerts } from './useAlerts';
import type { AlertsState } from './useAlerts';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function fetchMock(): ReturnType<typeof vi.fn> {
  return globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
}

function apiAlert(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: '1',
    fingerprint: 'fp-1',
    source: 'am-eu',
    status: 'firing',
    severity: 'critical',
    labels: { alertname: 'HighErrorRate', team: 'core' },
    annotations: { summary: 'Error rate above 5% for 10m' },
    startsAt: '2026-08-14T10:00:00Z',
    endsAt: null,
    ...overrides,
  };
}

function alertsPage(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    alerts: [],
    nextCursor: '',
    severityCounts: {},
    total: 0,
    streamCursor: 7,
    ...overrides,
  };
}

function alertsFetchCalls(): string[] {
  return fetchMock()
    .mock.calls.map(([url]) => String(url))
    .filter((url) => url.startsWith('/api/v1/alerts'));
}

function readyData(state: AlertsState): Extract<AlertsState, { status: 'ready' }>['data'] {
  if (state.status !== 'ready') {
    throw new Error(`Expected ready state, got ${state.status}`);
  }
  return state.data;
}

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn());
});

afterEach(() => {
  // Unmount before the stubs go away; a hook still mounted when fetch
  // disappears throws against whichever test runs next.
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('useAlerts live refresh', () => {
  it('stores the snapshot streamCursor with the first page', async () => {
    fetchMock().mockResolvedValue(
      jsonResponse(alertsPage({ alerts: [apiAlert()], total: 1, streamCursor: 7 })),
    );
    const { result } = renderHook(() => useAlerts(true, {}, { liveRefreshDebounceMs: 0 }));

    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    expect(readyData(result.current.state).streamCursor).toBe(7);
  });

  it('coalesces a burst of events into one quiet refresh that replaces rows and totals', async () => {
    const firstPage = alertsPage({
      alerts: [apiAlert({ id: '1' })],
      severityCounts: { critical: 1 },
      total: 1,
      streamCursor: 7,
    });
    const refreshedPage = alertsPage({
      alerts: [
        apiAlert({ id: '1' }),
        apiAlert({
          id: '2',
          severity: 'warning',
          labels: { alertname: 'DiskFull', severity: 'warning' },
        }),
      ],
      severityCounts: { critical: 1, warning: 1 },
      total: 2,
      streamCursor: 9,
    });
    let alertFetches = 0;
    fetchMock().mockImplementation(() => {
      alertFetches += 1;
      return Promise.resolve(jsonResponse(alertFetches === 1 ? firstPage : refreshedPage));
    });
    const seenStatuses: string[] = [];
    const { result } = renderHook(() => {
      const hook = useAlerts(true, {}, { liveRefreshDebounceMs: 0 });
      seenStatuses.push(hook.state.status);
      return hook;
    });

    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    expect(readyData(result.current.state).alerts).toHaveLength(1);

    // A burst of stream events inside the debounce window fetches once.
    act(() => {
      result.current.scheduleLiveRefresh();
      result.current.scheduleLiveRefresh();
      result.current.scheduleLiveRefresh();
    });

    await waitFor(() => expect(readyData(result.current.state).alerts).toHaveLength(2));
    expect(alertsFetchCalls()).toEqual([
      '/api/v1/alerts?limit=100&status=firing',
      '/api/v1/alerts?limit=100&status=firing',
    ]);

    const data = readyData(result.current.state);
    expect(data.alerts.map((alert) => alert.name)).toEqual(['HighErrorRate', 'DiskFull']);
    expect(data.total).toBe(2);
    expect(data.severityCounts).toEqual({ critical: 1, warning: 1 });
    expect(data.streamCursor).toBe(9);
    // Quiet refresh: the hook never fell back to the initial loading state.
    expect(seenStatuses).not.toContain('error');
    const firstReady = seenStatuses.indexOf('ready');
    expect(firstReady).toBeGreaterThan(-1);
    expect(seenStatuses.slice(firstReady)).not.toContain('loading');
  });

  it('runs one trailing refresh when events arrive while a refresh is in flight', async () => {
    let alertFetches = 0;
    let resolveRefresh: (response: Response) => void = () => {};
    fetchMock().mockImplementation(() => {
      alertFetches += 1;
      if (alertFetches === 2) {
        return new Promise<Response>((resolve) => {
          resolveRefresh = resolve;
        });
      }
      return Promise.resolve(jsonResponse(alertsPage({ total: alertFetches })));
    });
    const { result } = renderHook(() => useAlerts(true, {}, { liveRefreshDebounceMs: 0 }));

    await waitFor(() => expect(result.current.state.status).toBe('ready'));

    // First event: refresh starts and hangs in flight.
    act(() => result.current.scheduleLiveRefresh());
    await waitFor(() => expect(alertsFetchCalls()).toHaveLength(2));

    // A burst lands while the request is in flight: exactly one trailing run.
    act(() => {
      result.current.scheduleLiveRefresh();
      result.current.scheduleLiveRefresh();
    });
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 10));
    });
    expect(alertsFetchCalls()).toHaveLength(2);

    await act(async () => {
      resolveRefresh(jsonResponse(alertsPage({ total: 5, streamCursor: 8 })));
    });
    await waitFor(() => expect(alertsFetchCalls()).toHaveLength(3));
    await waitFor(() => expect(readyData(result.current.state).total).toBe(3));
  });

  it('keeps the current rows when the quiet refresh fails', async () => {
    let call = 0;
    fetchMock().mockImplementation(() => {
      call += 1;
      return call === 1
        ? Promise.resolve(
            jsonResponse(alertsPage({ alerts: [apiAlert()], total: 1, streamCursor: 7 })),
          )
        : Promise.reject(new TypeError('fetch failed'));
    });
    const { result } = renderHook(() => useAlerts(true, {}, { liveRefreshDebounceMs: 0 }));

    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    act(() => result.current.scheduleLiveRefresh());

    await waitFor(() => expect(alertsFetchCalls()).toHaveLength(2));
    expect(result.current.state.status).toBe('ready');
    expect(readyData(result.current.state).alerts).toHaveLength(1);
    expect(readyData(result.current.state).total).toBe(1);
  });

  it('keeps pagination working from the refreshed first page', async () => {
    const firstPage = alertsPage({
      alerts: [apiAlert({ id: '1' })],
      nextCursor: 'cursor-2',
      total: 3,
      streamCursor: 7,
    });
    const refreshedPage = alertsPage({
      alerts: [apiAlert({ id: '1' }), apiAlert({ id: '2', labels: { alertname: 'DiskFull' } })],
      nextCursor: 'cursor-3',
      total: 3,
      streamCursor: 9,
    });
    let alertFetches = 0;
    fetchMock().mockImplementation((url: string) => {
      const target = String(url);
      alertFetches += 1;
      if (target.includes('cursor=cursor-3')) {
        return Promise.resolve(
          jsonResponse(alertsPage({ alerts: [apiAlert({ id: '3' })], total: 3, streamCursor: 9 })),
        );
      }
      return Promise.resolve(jsonResponse(alertFetches === 1 ? firstPage : refreshedPage));
    });
    const { result } = renderHook(() => useAlerts(true, {}, { liveRefreshDebounceMs: 0 }));

    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    act(() => result.current.scheduleLiveRefresh());
    await waitFor(() => expect(readyData(result.current.state).alerts).toHaveLength(2));
    expect(readyData(result.current.state).nextCursor).toBe('cursor-3');

    act(() => result.current.loadMore());
    await waitFor(() => expect(readyData(result.current.state).alerts).toHaveLength(3));
    expect(alertsFetchCalls()).toContain('/api/v1/alerts?limit=100&cursor=cursor-3&status=firing');
    expect(readyData(result.current.state).nextCursor).toBe('');
  });

  it('cancels a pending live-refresh timer on unmount', async () => {
    vi.useFakeTimers();
    fetchMock().mockResolvedValue(jsonResponse(alertsPage({ total: 1, streamCursor: 7 })));
    const { result, unmount } = renderHook(() =>
      useAlerts(true, {}, { liveRefreshDebounceMs: 500 }),
    );

    await act(async () => {});
    expect(result.current.state.status).toBe('ready');
    expect(alertsFetchCalls()).toHaveLength(1);

    act(() => result.current.scheduleLiveRefresh());
    unmount();
    await act(async () => {
      vi.advanceTimersByTime(2000);
    });

    expect(alertsFetchCalls()).toHaveLength(1);
  });
});

describe('useAlerts session expiry', () => {
  it('reports a 401 from the first page fetch', async () => {
    fetchMock().mockResolvedValue(jsonResponse({ error: 'expired' }, 401));
    const onUnauthorized = vi.fn();
    const { result } = renderHook(() => useAlerts(true, {}, { onUnauthorized }));

    await waitFor(() => expect(result.current.state.status).toBe('error'));
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });

  it('does not report other failures as expiry', async () => {
    fetchMock().mockResolvedValue(jsonResponse({ error: 'boom' }, 500));
    const onUnauthorized = vi.fn();
    const { result } = renderHook(() => useAlerts(true, {}, { onUnauthorized }));

    await waitFor(() => expect(result.current.state.status).toBe('error'));
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it('reports a 401 from the quiet live refresh and keeps the rows', async () => {
    let call = 0;
    fetchMock().mockImplementation(() => {
      call += 1;
      return call === 1
        ? Promise.resolve(jsonResponse(alertsPage({ alerts: [apiAlert()], total: 1 })))
        : Promise.resolve(jsonResponse({ error: 'expired' }, 401));
    });
    const onUnauthorized = vi.fn();
    const { result } = renderHook(() =>
      useAlerts(true, {}, { liveRefreshDebounceMs: 0, onUnauthorized }),
    );

    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    act(() => result.current.scheduleLiveRefresh());

    await waitFor(() => expect(onUnauthorized).toHaveBeenCalledTimes(1));
    expect(result.current.state.status).toBe('ready');
    expect(readyData(result.current.state).alerts).toHaveLength(1);
  });

  it('reports a 401 from cursor pagination', async () => {
    let call = 0;
    fetchMock().mockImplementation(() => {
      call += 1;
      return call === 1
        ? Promise.resolve(
            jsonResponse(alertsPage({ alerts: [apiAlert()], nextCursor: 'cursor-2', total: 2 })),
          )
        : Promise.resolve(jsonResponse({ error: 'expired' }, 401));
    });
    const onUnauthorized = vi.fn();
    const { result } = renderHook(() =>
      useAlerts(true, {}, { liveRefreshDebounceMs: 0, onUnauthorized }),
    );

    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    act(() => result.current.loadMore());

    await waitFor(() => expect(onUnauthorized).toHaveBeenCalledTimes(1));
  });

  it('resets to a clean loading state when the gate closes and re-opens', async () => {
    fetchMock().mockImplementation(() =>
      Promise.resolve(jsonResponse(alertsPage({ alerts: [apiAlert()], total: 1 }))),
    );
    const { result, rerender } = renderHook(
      ({ enabled }: { enabled: boolean }) => useAlerts(enabled, {}, { liveRefreshDebounceMs: 0 }),
      { initialProps: { enabled: true } },
    );

    await waitFor(() => expect(result.current.state.status).toBe('ready'));

    // Session lost: loaded rows and cursors are dropped.
    rerender({ enabled: false });
    expect(result.current.state.status).toBe('loading');

    // Re-unlocking starts from a fresh first page.
    rerender({ enabled: true });
    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    expect(alertsFetchCalls()).toHaveLength(2);
  });
});

describe('useAlerts server-side query', () => {
  it('threads match and sort params into every request', async () => {
    fetchMock().mockImplementation((url: string) =>
      Promise.resolve(
        String(url).includes('cursor=cursor-2')
          ? jsonResponse(alertsPage({ alerts: [apiAlert({ id: '2' })], total: 2 }))
          : jsonResponse(alertsPage({ alerts: [apiAlert()], nextCursor: 'cursor-2', total: 2 })),
      ),
    );
    const query = {
      match: ['team=core', 'severity!=info'],
      sort: 'age' as const,
      order: 'desc' as const,
    };
    const { result } = renderHook(() => useAlerts(true, query, { liveRefreshDebounceMs: 0 }));

    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    act(() => result.current.loadMore());
    await waitFor(() => expect(readyData(result.current.state).nextCursor).toBe(''));
    act(() => result.current.scheduleLiveRefresh());
    await waitFor(() => expect(alertsFetchCalls()).toHaveLength(3));

    const suffix = 'match=team%3Dcore&match=severity%21%3Dinfo&sort=startsAt&order=asc';
    expect(alertsFetchCalls()).toEqual([
      `/api/v1/alerts?limit=100&status=firing&${suffix}`,
      `/api/v1/alerts?limit=100&cursor=cursor-2&status=firing&${suffix}`,
      `/api/v1/alerts?limit=100&status=firing&${suffix}`,
    ]);
  });

  it('restarts from the first page when the query content changes', async () => {
    fetchMock().mockImplementation(() =>
      Promise.resolve(jsonResponse(alertsPage({ alerts: [apiAlert()], total: 1 }))),
    );
    const { result, rerender } = renderHook(
      ({ query }: { query: { match?: string[] } }) =>
        useAlerts(true, query, { liveRefreshDebounceMs: 0 }),
      { initialProps: { query: { match: ['team=core'] } } },
    );

    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    rerender({ query: { match: ['team=infra'] } });
    await waitFor(() => expect(alertsFetchCalls()).toHaveLength(2));

    expect(alertsFetchCalls()).toEqual([
      '/api/v1/alerts?limit=100&status=firing&match=team%3Dcore',
      '/api/v1/alerts?limit=100&status=firing&match=team%3Dinfra',
    ]);
  });

  it('ignores identity-only query changes', async () => {
    fetchMock().mockImplementation(() =>
      Promise.resolve(jsonResponse(alertsPage({ alerts: [apiAlert()], total: 1 }))),
    );
    const { result, rerender } = renderHook(
      ({ query }: { query: { match: string[] } }) =>
        useAlerts(true, query, { liveRefreshDebounceMs: 0 }),
      { initialProps: { query: { match: ['team=core'] } } },
    );

    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    // Same content, new object: no refetch.
    rerender({ query: { match: ['team=core'] } });
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 10));
    });
    expect(alertsFetchCalls()).toHaveLength(1);
  });

  it('drops a pending page when the query changes mid-pagination', async () => {
    let resolveSecondPage: (response: Response) => void = () => {};
    fetchMock().mockImplementation((url: string) => {
      const target = String(url);
      if (target.includes('cursor=cursor-2')) {
        return new Promise<Response>((resolve) => {
          resolveSecondPage = resolve;
        });
      }
      return Promise.resolve(
        jsonResponse(alertsPage({ alerts: [apiAlert()], nextCursor: 'cursor-2', total: 2 })),
      );
    });
    const { result, rerender } = renderHook(
      ({ query }: { query: { match?: string[] } }) =>
        useAlerts(true, query, { liveRefreshDebounceMs: 0 }),
      { initialProps: { query: {} } },
    );

    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    act(() => result.current.loadMore());
    await waitFor(() => expect(alertsFetchCalls()).toHaveLength(2));

    // The filter changes while page two is in flight: a fresh first page
    // goes out with the new matcher, and the stale page response is ignored.
    rerender({ query: { match: ['team=core'] } });
    await waitFor(() => expect(alertsFetchCalls()).toHaveLength(3));
    expect(alertsFetchCalls()[2]).toBe('/api/v1/alerts?limit=100&status=firing&match=team%3Dcore');

    await act(async () => {
      resolveSecondPage(jsonResponse(alertsPage({ alerts: [apiAlert({ id: '2' })], total: 2 })));
    });
    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    expect(readyData(result.current.state).alerts.map((alert) => alert.id)).toEqual(['1']);
  });
});
