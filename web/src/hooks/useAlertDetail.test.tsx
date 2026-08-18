import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useAlertDetail } from './useAlertDetail';
import type { AlertDetailState } from './useAlertDetail';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function fetchMock(): ReturnType<typeof vi.fn> {
  return globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
}

function apiHistoryEvent(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: 11,
    occurrence: 2,
    type: 'updated',
    sourceStatus: 'firing',
    actor: 'alertmanager',
    message: 'Notification sent',
    occurredAt: '2026-08-14T11:00:00Z',
    ...overrides,
  };
}

function apiAlertDetail(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: '42',
    fingerprint: 'fp-42',
    source: 'am-eu',
    status: 'firing',
    severity: 'critical',
    labels: { alertname: 'HighErrorRate' },
    annotations: { summary: 'Error rate above 5% for 10m' },
    startsAt: '2026-08-14T10:00:00Z',
    endsAt: null,
    generatorURL: 'http://prometheus/graph',
    externalURL: 'http://alertmanager',
    firstSeen: '2026-08-14T10:00:00Z',
    lastSeen: '2026-08-14T11:00:00Z',
    repeatCount: 3,
    occurrence: 2,
    rawData: { status: 'firing' },
    ...overrides,
  };
}

function apiDetailResponse(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return { alert: apiAlertDetail(), history: [apiHistoryEvent()], ...overrides };
}

function readyDetail(state: AlertDetailState) {
  if (state.status !== 'ready') {
    throw new Error(`Expected ready state, got ${state.status}`);
  }
  return state.detail;
}

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn());
});

afterEach(() => {
  // Unmount before the stubs go away; a hook still mounted when fetch
  // disappears throws against whichever test runs next.
  cleanup();
  vi.unstubAllGlobals();
});

describe('useAlertDetail', () => {
  it('stays idle without a selection and loads when one arrives', async () => {
    fetchMock().mockResolvedValue(jsonResponse(apiDetailResponse()));
    const { result, rerender } = renderHook(
      ({ alertId }: { alertId: string | null }) => useAlertDetail(alertId),
      { initialProps: { alertId: null as string | null } },
    );

    expect(result.current.state.status).toBe('idle');
    expect(fetchMock()).not.toHaveBeenCalled();

    rerender({ alertId: '42' });
    expect(result.current.state.status).toBe('loading');

    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    expect(readyDetail(result.current.state).alert.name).toBe('HighErrorRate');
    expect(fetchMock()).toHaveBeenCalledWith('/api/v1/alerts/42');
  });

  it('returns to idle when the selection clears', async () => {
    fetchMock().mockResolvedValue(jsonResponse(apiDetailResponse()));
    const { result, rerender } = renderHook(
      ({ alertId }: { alertId: string | null }) => useAlertDetail(alertId),
      { initialProps: { alertId: '42' as string | null } },
    );

    await waitFor(() => expect(result.current.state.status).toBe('ready'));

    rerender({ alertId: null });
    expect(result.current.state.status).toBe('idle');
  });

  it('reloads when the selection changes to another alert', async () => {
    fetchMock().mockImplementation((url: string) =>
      Promise.resolve(
        jsonResponse(
          apiDetailResponse(
            String(url).endsWith('/7')
              ? { alert: apiAlertDetail({ id: '7', labels: { alertname: 'DiskFull' } }) }
              : {},
          ),
        ),
      ),
    );
    const { result, rerender } = renderHook(
      ({ alertId }: { alertId: string | null }) => useAlertDetail(alertId),
      { initialProps: { alertId: '42' as string | null } },
    );
    await waitFor(() => expect(result.current.state.status).toBe('ready'));

    rerender({ alertId: '7' });

    expect(result.current.state.status).toBe('loading');
    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    expect(readyDetail(result.current.state).alert.name).toBe('DiskFull');
  });

  it('surfaces request errors and retries', async () => {
    fetchMock()
      .mockRejectedValueOnce(new TypeError('fetch failed'))
      .mockResolvedValue(jsonResponse(apiDetailResponse()));
    const { result } = renderHook(() => useAlertDetail('42'));

    await waitFor(() => expect(result.current.state.status).toBe('error'));

    act(() => result.current.retry());

    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    expect(fetchMock()).toHaveBeenCalledTimes(2);
  });

  it('maps HTTP 404 to the not-found state and can retry from it', async () => {
    fetchMock()
      .mockResolvedValueOnce(jsonResponse({ error: 'nope' }, 404))
      .mockResolvedValue(jsonResponse(apiDetailResponse()));
    const { result } = renderHook(() => useAlertDetail('42'));

    await waitFor(() => expect(result.current.state.status).toBe('not-found'));

    act(() => result.current.retry());

    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    expect(fetchMock()).toHaveBeenCalledTimes(2);
  });

  it('reports a 401 as session expiry', async () => {
    fetchMock().mockResolvedValue(jsonResponse({ error: 'expired' }, 401));
    const onUnauthorized = vi.fn();
    const { result } = renderHook(() => useAlertDetail('42', { onUnauthorized }));

    await waitFor(() => expect(result.current.state.status).toBe('error'));
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });

  it('reports a 401 from the quiet refresh as session expiry', async () => {
    fetchMock().mockResolvedValueOnce(jsonResponse(apiDetailResponse()));
    const onUnauthorized = vi.fn();
    const { result } = renderHook(() => useAlertDetail('42', { onUnauthorized }));
    await waitFor(() => expect(result.current.state.status).toBe('ready'));

    fetchMock().mockResolvedValueOnce(jsonResponse({ error: 'expired' }, 401));
    act(() => result.current.refreshIfSelected('42'));

    await waitFor(() => expect(onUnauthorized).toHaveBeenCalledTimes(1));
    // The stale detail stays put; the shell handles the gate transition.
    expect(result.current.state.status).toBe('ready');
  });

  it('quietly refreshes when a stream event targets the selected alert', async () => {
    const stale = apiDetailResponse();
    const fresh = apiDetailResponse({
      alert: apiAlertDetail({ status: 'resolved', endsAt: '2026-08-14T12:00:00Z' }),
      history: [
        apiHistoryEvent({ id: 12, type: 'resolved', occurredAt: '2026-08-14T12:00:00Z' }),
        apiHistoryEvent(),
      ],
    });
    let fetches = 0;
    fetchMock().mockImplementation(() => {
      fetches += 1;
      return Promise.resolve(jsonResponse(fetches === 1 ? stale : fresh));
    });
    const seenStatuses: string[] = [];
    const { result } = renderHook(() => {
      const hook = useAlertDetail('42');
      seenStatuses.push(hook.state.status);
      return hook;
    });
    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    // Quiet refresh: the loading state must not reappear after this point.
    seenStatuses.length = 0;

    act(() => result.current.refreshIfSelected('42'));

    await waitFor(() => expect(readyDetail(result.current.state).alert.status).toBe('resolved'));
    expect(seenStatuses).not.toContain('loading');
    expect(readyDetail(result.current.state).history).toHaveLength(2);
    expect(fetchMock()).toHaveBeenCalledTimes(2);
  });

  it('ignores stream events for other alerts', async () => {
    fetchMock().mockResolvedValue(jsonResponse(apiDetailResponse()));
    const { result } = renderHook(() => useAlertDetail('42'));
    await waitFor(() => expect(result.current.state.status).toBe('ready'));

    act(() => result.current.refreshIfSelected('someone-else'));

    expect(fetchMock()).toHaveBeenCalledTimes(1);
  });

  it('keeps the stale detail when a quiet refresh fails', async () => {
    fetchMock().mockResolvedValueOnce(jsonResponse(apiDetailResponse()));
    const { result } = renderHook(() => useAlertDetail('42'));
    await waitFor(() => expect(result.current.state.status).toBe('ready'));

    fetchMock().mockRejectedValueOnce(new TypeError('fetch failed'));
    act(() => result.current.refreshIfSelected('42'));

    await waitFor(() => expect(fetchMock()).toHaveBeenCalledTimes(2));
    expect(result.current.state.status).toBe('ready');
    expect(readyDetail(result.current.state).alert.name).toBe('HighErrorRate');
  });

  it('acknowledges the selected alert and replaces the detail with the response', async () => {
    fetchMock().mockResolvedValueOnce(jsonResponse(apiDetailResponse()));
    const { result } = renderHook(() => useAlertDetail('42'));
    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    expect(readyDetail(result.current.state).alert.acknowledged).toBe(false);

    fetchMock().mockResolvedValueOnce(
      jsonResponse(
        apiDetailResponse({
          alert: apiAlertDetail({
            acknowledged: true,
            acknowledgedBy: 'operator@example.com',
            acknowledgedAt: '2026-08-14T11:05:00Z',
            actions: { canAcknowledge: true },
          }),
          history: [apiHistoryEvent({ id: 12, type: 'acknowledged' }), apiHistoryEvent()],
        }),
      ),
    );
    await act(async () => {
      await result.current.acknowledge(true);
    });

    expect(fetchMock()).toHaveBeenCalledTimes(2);
    expect(fetchMock()).toHaveBeenLastCalledWith('/api/v1/alerts/42/acknowledge', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ acknowledged: true }),
    });
    const detail = readyDetail(result.current.state);
    expect(detail.alert.acknowledged).toBe(true);
    expect(detail.alert.acknowledgedBy).toBe('operator@example.com');
    // The endpoint returns the refreshed history; it replaces the old list.
    expect(detail.history.map((event) => event.type)).toEqual(['acknowledged', 'updated']);
  });

  it('keeps the detail and rethrows when the acknowledge request fails', async () => {
    fetchMock().mockResolvedValueOnce(jsonResponse(apiDetailResponse()));
    const { result } = renderHook(() => useAlertDetail('42'));
    await waitFor(() => expect(result.current.state.status).toBe('ready'));

    fetchMock().mockResolvedValueOnce(jsonResponse({ error: 'forbidden' }, 403));
    await act(async () => {
      await expect(result.current.acknowledge(true)).rejects.toThrowError(/HTTP 403/);
    });

    const detail = readyDetail(result.current.state);
    expect(detail.alert.acknowledged).toBe(false);
  });

  it('reports a 401 from the acknowledge request as session expiry', async () => {
    fetchMock().mockResolvedValueOnce(jsonResponse(apiDetailResponse()));
    const onUnauthorized = vi.fn();
    const { result } = renderHook(() => useAlertDetail('42', { onUnauthorized }));
    await waitFor(() => expect(result.current.state.status).toBe('ready'));

    fetchMock().mockResolvedValueOnce(jsonResponse({ error: 'expired' }, 401));
    await act(async () => {
      await expect(result.current.acknowledge(true)).rejects.toThrowError(/HTTP 401/);
    });

    expect(onUnauthorized).toHaveBeenCalledTimes(1);
    expect(result.current.state.status).toBe('ready');
  });
});
