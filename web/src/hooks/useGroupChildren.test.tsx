import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useGroupChildren } from './useGroupChildren';
import type { GroupChildren } from './useGroupChildren';

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
    source: 'yul',
    status: 'firing',
    severity: 'critical',
    labels: { alertname: 'Cardinality', instance: 'a' },
    annotations: { summary: 'too many series' },
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

function alertCalls(): string[] {
  return fetchMock()
    .mock.calls.map(([url]) => String(url))
    .filter((url) => url.startsWith('/api/v1/alerts'));
}

const KEY = { alertname: 'Cardinality', source: 'yul' };
const KEY_ID = JSON.stringify(KEY);

function loaded(children: Record<string, GroupChildren>, id: string = KEY_ID): GroupChildren {
  const entry = children[id];
  if (entry === undefined) {
    throw new Error(`Expected children for ${id}`);
  }
  return entry;
}

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn());
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('useGroupChildren', () => {
  it('loads a group’s members with the ordinary query plus the key matchers', async () => {
    fetchMock().mockImplementation(() =>
      Promise.resolve(jsonResponse(alertsPage({ alerts: [apiAlert()], total: 1 }))),
    );
    const { result } = renderHook(() => useGroupChildren({}));

    act(() => result.current.expand(KEY));

    await waitFor(() => expect(loaded(result.current.children).alerts).toHaveLength(1));
    expect(alertCalls()).toEqual([
      '/api/v1/alerts?limit=100&status=firing&source=yul&match=alertname%3DCardinality',
    ]);
  });

  it('requests firing members, matching the flat view', async () => {
    // A group drawn from firing alerts must not open into a wider set: the
    // flat list only ever shows firing, and the members are the same query.
    fetchMock().mockImplementation(() =>
      Promise.resolve(jsonResponse(alertsPage({ alerts: [apiAlert()], total: 1 }))),
    );
    const { result } = renderHook(() => useGroupChildren({}));

    act(() => result.current.expand(KEY));

    await waitFor(() => expect(loaded(result.current.children).alerts).toHaveLength(1));
    expect(alertCalls()[0]).toContain('status=firing');
  });

  it('discards expanded groups when the grouping keys change', async () => {
    // An expanded group's matchers name the old keys; under a new grouping
    // they would pin stale members to headings that mean something else.
    fetchMock().mockImplementation(() =>
      Promise.resolve(jsonResponse(alertsPage({ alerts: [apiAlert()], total: 1 }))),
    );
    const { result, rerender } = renderHook(({ groupBy }) => useGroupChildren({}, { groupBy }), {
      initialProps: { groupBy: ['alertname', 'source'] },
    });

    act(() => result.current.expand(KEY));
    await waitFor(() => expect(loaded(result.current.children).alerts).toHaveLength(1));

    rerender({ groupBy: ['team'] });

    expect(result.current.children).toEqual({});
  });

  it('keeps expanded groups when the grouping keys stay the same', async () => {
    fetchMock().mockImplementation(() =>
      Promise.resolve(jsonResponse(alertsPage({ alerts: [apiAlert()], total: 1 }))),
    );
    const { result, rerender } = renderHook(({ groupBy }) => useGroupChildren({}, { groupBy }), {
      initialProps: { groupBy: ['alertname', 'source'] },
    });

    act(() => result.current.expand(KEY));
    await waitFor(() => expect(loaded(result.current.children).alerts).toHaveLength(1));

    // A re-render with an equal list (a fresh array, as App rebuilds it)
    // must not collapse anything.
    rerender({ groupBy: ['alertname', 'source'] });

    expect(loaded(result.current.children).alerts).toHaveLength(1);
  });

  it('refreshes expanded groups in place instead of collapsing them', async () => {
    let call = 0;
    fetchMock().mockImplementation(() => {
      call += 1;
      return Promise.resolve(
        jsonResponse(
          call === 1
            ? alertsPage({
                alerts: [apiAlert({ id: '1' }), apiAlert({ id: '2' })],
                total: 2,
              })
            : alertsPage({
                alerts: [apiAlert({ id: '1' }), apiAlert({ id: '3' })],
                total: 2,
              }),
        ),
      );
    });
    const { result } = renderHook(() => useGroupChildren({}));

    act(() => result.current.expand(KEY));
    await waitFor(() => expect(loaded(result.current.children).alerts).toHaveLength(2));

    act(() => result.current.refresh());

    // The group stays expanded throughout; only the members swap.
    expect(result.current.children[KEY_ID]).toBeDefined();
    await waitFor(() =>
      expect(loaded(result.current.children).alerts.map((alert) => alert.id)).toEqual(['1', '3']),
    );
    expect(alertCalls()).toHaveLength(2);
    expect(alertCalls()[1]).not.toContain('cursor=');
  });

  it('refreshes every expanded group, keyed by its own matchers', async () => {
    fetchMock().mockImplementation(() =>
      Promise.resolve(jsonResponse(alertsPage({ alerts: [apiAlert()], total: 1 }))),
    );
    const other = { alertname: 'DiskFull', source: 'yul' };
    const { result } = renderHook(() => useGroupChildren({}));

    act(() => {
      result.current.expand(KEY);
      result.current.expand(other);
    });
    await waitFor(() => expect(alertCalls()).toHaveLength(2));

    act(() => result.current.refresh());
    await waitFor(() => expect(alertCalls()).toHaveLength(4));

    expect(alertCalls()[2]).toBe(
      '/api/v1/alerts?limit=100&status=firing&source=yul&match=alertname%3DCardinality',
    );
    expect(alertCalls()[3]).toBe(
      '/api/v1/alerts?limit=100&status=firing&source=yul&match=alertname%3DDiskFull',
    );
    expect(loaded(result.current.children).alerts).toHaveLength(1);
    expect(loaded(result.current.children, JSON.stringify(other)).alerts).toHaveLength(1);
  });

  it('keeps the current members when a background refresh fails', async () => {
    let call = 0;
    fetchMock().mockImplementation(() => {
      call += 1;
      return call === 1
        ? Promise.resolve(jsonResponse(alertsPage({ alerts: [apiAlert()], total: 1 })))
        : Promise.reject(new TypeError('fetch failed'));
    });
    const { result } = renderHook(() => useGroupChildren({}));

    act(() => result.current.expand(KEY));
    await waitFor(() => expect(loaded(result.current.children).alerts).toHaveLength(1));

    act(() => result.current.refresh());
    await waitFor(() => expect(alertCalls()).toHaveLength(2));

    // Quiet failure: the group stays expanded with its stale members and no
    // error row; the next stream event retries.
    const entry = loaded(result.current.children);
    expect(entry.alerts).toHaveLength(1);
    expect(entry.error).toBeNull();
  });

  it('drops a refresh result for a group collapsed while in flight', async () => {
    let resolveMembers: (response: Response) => void = () => {};
    fetchMock().mockImplementation(
      () =>
        new Promise<Response>((resolve) => {
          resolveMembers = resolve;
        }),
    );
    const { result } = renderHook(() => useGroupChildren({}));

    act(() => result.current.expand(KEY));
    await waitFor(() => expect(alertCalls()).toHaveLength(1));
    act(() => result.current.collapse(KEY));
    expect(result.current.children[KEY_ID]).toBeUndefined();

    await act(async () => {
      resolveMembers(jsonResponse(alertsPage({ alerts: [apiAlert()], total: 1 })));
    });
    expect(result.current.children[KEY_ID]).toBeUndefined();
  });
});
