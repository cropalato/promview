import { act, renderHook, waitFor } from '@testing-library/react';
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
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('useAlerts live refresh', () => {
  it('stores the snapshot streamCursor with the first page', async () => {
    fetchMock().mockResolvedValue(
      jsonResponse(alertsPage({ alerts: [apiAlert()], total: 1, streamCursor: 7 })),
    );
    const { result } = renderHook(() => useAlerts(true, { liveRefreshDebounceMs: 0 }));

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
      const hook = useAlerts(true, { liveRefreshDebounceMs: 0 });
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
    const { result } = renderHook(() => useAlerts(true, { liveRefreshDebounceMs: 0 }));

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
    const { result } = renderHook(() => useAlerts(true, { liveRefreshDebounceMs: 0 }));

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
    const { result } = renderHook(() => useAlerts(true, { liveRefreshDebounceMs: 0 }));

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
    const { result, unmount } = renderHook(() => useAlerts(true, { liveRefreshDebounceMs: 500 }));

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
