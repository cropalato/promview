import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useAlertGroups } from './useAlertGroups';
import type { AlertGroupsState } from './useAlertGroups';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function fetchMock(): ReturnType<typeof vi.fn> {
  return globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
}

function apiGroup(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    key: { alertname: 'Cardinality', source: 'yul' },
    total: 52,
    acknowledged: 0,
    severityCounts: { critical: 1, warning: 51 },
    worstSeverity: 'critical',
    latestLastSeen: '2026-08-18T12:00:00Z',
    earliestStartsAt: '2026-08-18T11:00:00Z',
    sampleAlertId: '42',
    ...overrides,
  };
}

function groupsPage(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    groups: [],
    nextCursor: '',
    severityCounts: {},
    total: 0,
    totalGroups: 0,
    streamCursor: 7,
    ...overrides,
  };
}

function groupCalls(): string[] {
  return fetchMock()
    .mock.calls.map(([url]) => String(url))
    .filter((url) => url.startsWith('/api/v1/alerts'));
}

function readyData(
  state: AlertGroupsState,
): Extract<AlertGroupsState, { status: 'ready' }>['data'] {
  if (state.status !== 'ready') {
    throw new Error(`Expected ready state, got ${state.status}`);
  }
  return state.data;
}

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn());
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('useAlertGroups refresh', () => {
  it('asks for firing alerts, matching the flat view', async () => {
    // The two views are the same query in two shapes; without the status the
    // grouped view would count resolved and expired alerts the flat list
    // never shows.
    fetchMock().mockImplementation(() =>
      Promise.resolve(jsonResponse(groupsPage({ groups: [apiGroup()], totalGroups: 1 }))),
    );
    const { result } = renderHook(() =>
      useAlertGroups(true, { groupBy: ['alertname'], status: 'resolved' }),
    );

    await waitFor(() => expect(result.current.state.status).toBe('ready'));

    // Even a status in the caller's query is overridden: the console has no
    // grouped resolved view, and a split would confuse the two.
    expect(groupCalls()).toEqual(['/api/v1/alerts?limit=100&status=firing&groupBy=alertname']);
  });

  it('swaps refreshed groups in place without dropping back to loading', async () => {
    const firstPage = groupsPage({ groups: [apiGroup()], total: 52, totalGroups: 1 });
    const refreshedPage = groupsPage({
      groups: [
        apiGroup({ total: 51, acknowledged: 1 }),
        apiGroup({ key: { alertname: 'DiskFull', source: 'yul' }, total: 3, sampleAlertId: '7' }),
      ],
      total: 54,
      totalGroups: 2,
      streamCursor: 9,
    });
    let call = 0;
    fetchMock().mockImplementation(() => {
      call += 1;
      return Promise.resolve(jsonResponse(call === 1 ? firstPage : refreshedPage));
    });
    const seenStatuses: string[] = [];
    const { result } = renderHook(() => {
      const hook = useAlertGroups(true, { groupBy: ['alertname', 'source'] });
      seenStatuses.push(hook.state.status);
      return hook;
    });

    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    expect(readyData(result.current.state).groups).toHaveLength(1);

    act(() => result.current.refresh());

    await waitFor(() => expect(readyData(result.current.state).groups).toHaveLength(2));
    expect(readyData(result.current.state).streamCursor).toBe(9);
    expect(groupCalls()).toEqual([
      '/api/v1/alerts?limit=100&status=firing&groupBy=alertname%2Csource',
      '/api/v1/alerts?limit=100&status=firing&groupBy=alertname%2Csource',
    ]);
    // Quiet refresh: once ready the hook never re-showed the loading state.
    const firstReady = seenStatuses.indexOf('ready');
    expect(firstReady).toBeGreaterThan(-1);
    expect(seenStatuses.slice(firstReady)).not.toContain('loading');
  });

  it('keeps the current groups when the quiet refresh fails', async () => {
    let call = 0;
    fetchMock().mockImplementation(() => {
      call += 1;
      return call === 1
        ? Promise.resolve(jsonResponse(groupsPage({ groups: [apiGroup()], totalGroups: 1 })))
        : Promise.reject(new TypeError('fetch failed'));
    });
    const { result } = renderHook(() => useAlertGroups(true, { groupBy: ['alertname'] }));

    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    act(() => result.current.refresh());

    await waitFor(() => expect(groupCalls()).toHaveLength(2));
    expect(result.current.state.status).toBe('ready');
    expect(readyData(result.current.state).groups).toHaveLength(1);
  });

  it('reports a 401 from the quiet refresh and keeps the groups', async () => {
    let call = 0;
    fetchMock().mockImplementation(() => {
      call += 1;
      return call === 1
        ? Promise.resolve(jsonResponse(groupsPage({ groups: [apiGroup()], totalGroups: 1 })))
        : Promise.resolve(jsonResponse({ error: 'expired' }, 401));
    });
    const onUnauthorized = vi.fn();
    const { result } = renderHook(() =>
      useAlertGroups(true, { groupBy: ['alertname'] }, { onUnauthorized }),
    );

    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    act(() => result.current.refresh());

    await waitFor(() => expect(onUnauthorized).toHaveBeenCalledTimes(1));
    expect(result.current.state.status).toBe('ready');
    expect(readyData(result.current.state).groups).toHaveLength(1);
  });

  it('does nothing while disabled', async () => {
    fetchMock().mockResolvedValue(jsonResponse(groupsPage()));
    const { result } = renderHook(() => useAlertGroups(false, { groupBy: ['alertname'] }));

    act(() => result.current.refresh());
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 10));
    });

    expect(groupCalls()).toHaveLength(0);
    expect(result.current.state.status).toBe('loading');
  });
});
