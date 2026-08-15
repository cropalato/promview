import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import App from './App';
import { AutoOpenEventSource, FakeEventSource } from './test/fakeEventSource';

const OPEN_CONFIG = { authMode: 'open', productName: 'Promview' };

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
    labels: { alertname: 'HighErrorRate', team: 'core', instance: 'api-1:9090' },
    annotations: { summary: 'Error rate above 5% for 10m' },
    startsAt: '2026-08-14T10:00:00Z',
    endsAt: null,
    generatorURL: 'http://prometheus/graph',
    externalURL: 'http://alertmanager',
    firstSeen: '2026-08-14T10:00:00Z',
    lastSeen: '2026-08-14T11:00:00Z',
    repeatCount: 1,
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

/** Routes the fetch mock: the config endpoint gets OPEN_CONFIG, alerts get the page. */
function mockApi(page: unknown = alertsPage(), config: unknown = OPEN_CONFIG): void {
  fetchMock().mockImplementation((url: string) =>
    Promise.resolve(
      String(url).startsWith('/api/v1/alerts') ? jsonResponse(page) : jsonResponse(config),
    ),
  );
}

function apiHistoryEvent(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: 11,
    occurrence: 1,
    type: 'updated',
    sourceStatus: 'firing',
    actor: 'alertmanager',
    message: 'Notification sent to team-core',
    occurredAt: '2026-08-14T11:00:00Z',
    ...overrides,
  };
}

function apiDetailResponse(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    alert: {
      ...apiAlert(),
      repeatCount: 3,
      occurrence: 1,
      rawData: { status: 'firing' },
    },
    history: [apiHistoryEvent()],
    ...overrides,
  };
}

/** Routes the fetch mock with the alert detail endpoint served too. */
function mockApiWithDetail(
  page: unknown = alertsPage(),
  detail: unknown = apiDetailResponse(),
): void {
  fetchMock().mockImplementation((url: string) => {
    const target = String(url);
    if (target.startsWith('/api/v1/alerts/')) {
      return Promise.resolve(jsonResponse(detail));
    }
    if (target.startsWith('/api/v1/alerts')) {
      return Promise.resolve(jsonResponse(page));
    }
    return Promise.resolve(jsonResponse(OPEN_CONFIG));
  });
}

function alertCalls(): string[] {
  return fetchMock()
    .mock.calls.map(([url]) => String(url))
    .filter((url) => url.startsWith('/api/v1/alerts'));
}

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn());
  // The live stream connects through the global EventSource; the auto-opening
  // fake mirrors a healthy browser connect.
  vi.stubGlobal('EventSource', AutoOpenEventSource);
  FakeEventSource.reset();
  // Tests navigate via pushState; always start from the list route.
  window.history.replaceState(null, '', '/');
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  window.history.replaceState(null, '', '/');
});

describe('App', () => {
  it('loads runtime config, then the first page of firing alerts', async () => {
    mockApi();
    render(<App />);

    expect(screen.getByText(/connecting to the promview api/i)).toBeInTheDocument();

    const banner = screen.getByRole('banner');
    expect(await within(banner).findByText('Promview')).toBeInTheDocument();
    expect(within(banner).getByText('Open access')).toBeInTheDocument();
    expect(within(banner).getByText('Anonymous viewer')).toBeInTheDocument();
    expect(await within(banner).findByText('Connected')).toBeInTheDocument();
    expect(within(banner).getByText('viewer')).toBeInTheDocument();

    expect(screen.getByRole('heading', { level: 1, name: 'Alerts' })).toBeInTheDocument();
    expect(await screen.findByRole('heading', { name: /all clear/i })).toBeInTheDocument();
    expect(screen.getByRole('search', { name: /alert filter/i })).toBeInTheDocument();
    expect(screen.getByText('0 of 0 alerts')).toBeInTheDocument();
    expect(screen.getByText('0 firing')).toBeInTheDocument();

    expect(fetchMock()).toHaveBeenCalledWith('/api/v1/config');
    expect(alertCalls()).toEqual(['/api/v1/alerts?limit=100&status=firing']);
  });

  it('shows an error state and retries the config request', async () => {
    fetchMock().mockRejectedValueOnce(new TypeError('fetch failed'));
    render(<App />);

    const alertRegion = await screen.findByRole('alert');
    expect(alertRegion).toHaveTextContent(/cannot reach the promview api/i);
    expect(alertRegion).toHaveTextContent(/unable to reach the promview api/i);

    mockApi();
    fireEvent.click(screen.getByRole('button', { name: /retry connection/i }));

    expect(await screen.findByText('Connected')).toBeInTheDocument();
    expect(await screen.findByRole('heading', { name: /all clear/i })).toBeInTheDocument();
    expect(fetchMock()).toHaveBeenCalledTimes(3);
  });

  it('announces that ldap sign-in is not available yet', async () => {
    mockApi(alertsPage(), { authMode: 'ldap', productName: 'Promview' });
    render(<App />);

    expect(await screen.findByRole('note')).toHaveTextContent(/ldap sign-in/i);
    expect(screen.getByRole('banner')).toHaveTextContent('Sign-in pending');
    expect(await screen.findByRole('heading', { name: /all clear/i })).toBeInTheDocument();
    expect(await screen.findByText('Connected')).toBeInTheDocument();
  });

  it('switches the empty state when a filter is applied and cleared', async () => {
    mockApi();
    render(<App />);

    expect(await screen.findByText('Connected')).toBeInTheDocument();
    const input = await screen.findByRole('textbox', { name: /filter alerts/i });
    fireEvent.change(input, { target: { value: 'critical' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    expect(screen.getByRole('heading', { name: /no alerts match/i })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /clear filter/i }));
    expect(screen.getByRole('heading', { name: /all clear/i })).toBeInTheDocument();
    expect(input).toHaveValue('');
  });

  it('focuses the filter input when "/" is pressed', async () => {
    mockApi();
    render(<App />);

    expect(await screen.findByText('Connected')).toBeInTheDocument();
    const input = await screen.findByRole('textbox', { name: /filter alerts/i });
    expect(input).not.toHaveFocus();

    fireEvent.keyDown(document.body, { key: '/' });
    expect(input).toHaveFocus();
  });

  it('renders firing alerts with server-side totals, not just loaded rows', async () => {
    mockApi(
      alertsPage({
        alerts: [
          apiAlert({ id: '1' }),
          apiAlert({
            id: '2',
            severity: 'warning',
            labels: { alertname: 'DiskFull', severity: 'warning' },
            annotations: { description: 'Root filesystem above 90%' },
          }),
        ],
        severityCounts: { critical: 3, warning: 1 },
        total: 4,
      }),
    );
    render(<App />);

    expect(await screen.findByText('HighErrorRate')).toBeInTheDocument();
    expect(await screen.findByText('Connected')).toBeInTheDocument();
    expect(screen.getByText('DiskFull')).toBeInTheDocument();
    expect(screen.getByText('Root filesystem above 90%')).toBeInTheDocument();

    expect(screen.getByText('4 firing')).toBeInTheDocument();
    const [critical, warning] = screen.getAllByRole('listitem');
    expect(within(critical!).getByText('3')).toBeInTheDocument();
    expect(within(warning!).getByText('1')).toBeInTheDocument();

    expect(screen.getByText('Active alerts (2)')).toBeInTheDocument();
    expect(screen.getByText('2 of 4 alerts')).toBeInTheDocument();
    expect(screen.getByText(/showing 2 of 4 firing alerts/i)).toBeInTheDocument();
  });

  it('keeps the configured shell when alerts fail to load, and retries', async () => {
    fetchMock().mockImplementation((url: string) =>
      Promise.resolve(
        String(url).startsWith('/api/v1/alerts')
          ? jsonResponse({ error: 'boom' }, 503)
          : jsonResponse(OPEN_CONFIG),
      ),
    );
    render(<App />);

    const alertRegion = await screen.findByRole('alert');
    expect(alertRegion).toHaveTextContent(/cannot load alerts/i);
    expect(alertRegion).toHaveTextContent(/HTTP 503/);
    // The indicator follows live data, not just the loaded shell config.
    expect(screen.getByRole('banner')).toHaveTextContent('Offline');

    mockApi(alertsPage({ alerts: [apiAlert()], severityCounts: { critical: 1 }, total: 1 }));
    fireEvent.click(screen.getByRole('button', { name: /retry alerts request/i }));

    expect(await screen.findByText('HighErrorRate')).toBeInTheDocument();
    expect(await screen.findByText('Connected')).toBeInTheDocument();
    expect(alertCalls()).toHaveLength(2);
  });

  it('shows a loading state inside the configured shell', async () => {
    let resolveAlerts: (response: Response) => void = () => {};
    fetchMock().mockImplementation((url: string) => {
      if (String(url).startsWith('/api/v1/alerts')) {
        return new Promise<Response>((resolve) => {
          resolveAlerts = resolve;
        });
      }
      return Promise.resolve(jsonResponse(OPEN_CONFIG));
    });
    render(<App />);

    // Shell is configured but live data has not landed yet: still syncing.
    expect(await screen.findByText(/loading firing alerts/i)).toBeInTheDocument();
    expect(screen.getByText('Syncing')).toBeInTheDocument();
    expect(screen.getByRole('heading', { level: 1, name: 'Alerts' })).toBeInTheDocument();

    resolveAlerts(jsonResponse(alertsPage()));
    expect(await screen.findByRole('heading', { name: /all clear/i })).toBeInTheDocument();
    // The stream opens against the snapshot cursor and the bar goes live.
    expect(await screen.findByText('Connected')).toBeInTheDocument();
    expect(FakeEventSource.latest().url).toBe('/api/v1/stream?cursor=7');
  });

  it('pages with the server cursor and appends idempotently', async () => {
    const firstPage = alertsPage({
      alerts: [apiAlert({ id: '1' })],
      nextCursor: 'cursor-2',
      severityCounts: { critical: 2 },
      total: 2,
    });
    const secondPage = alertsPage({
      alerts: [
        apiAlert({ id: '1' }),
        apiAlert({
          id: '2',
          severity: 'warning',
          labels: { alertname: 'DiskFull', severity: 'warning' },
          annotations: { summary: 'Root filesystem above 90%' },
        }),
      ],
      nextCursor: '',
      severityCounts: { critical: 2 },
      total: 2,
    });
    fetchMock().mockImplementation((url: string) => {
      const target = String(url);
      if (target.startsWith('/api/v1/alerts')) {
        return Promise.resolve(
          jsonResponse(target.includes('cursor=cursor-2') ? secondPage : firstPage),
        );
      }
      return Promise.resolve(jsonResponse(OPEN_CONFIG));
    });
    render(<App />);

    expect(await screen.findByText('HighErrorRate')).toBeInTheDocument();
    expect(await screen.findByText('Connected')).toBeInTheDocument();
    expect(screen.getByText(/showing 1 of 2 firing alerts/i)).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /load more/i }));

    expect(await screen.findByText('DiskFull')).toBeInTheDocument();
    expect(screen.getAllByText('HighErrorRate')).toHaveLength(1);
    expect(screen.getByText(/showing 2 of 2 firing alerts/i)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /load more/i })).not.toBeInTheDocument();

    expect(alertCalls()).toEqual([
      '/api/v1/alerts?limit=100&status=firing',
      '/api/v1/alerts?limit=100&cursor=cursor-2&status=firing',
    ]);
  });

  it('keeps loaded rows when loading more fails, and retries the same cursor', async () => {
    let pageTwoAttempts = 0;
    const firstPage = alertsPage({
      alerts: [apiAlert({ id: '1' })],
      nextCursor: 'cursor-2',
      severityCounts: { critical: 2 },
      total: 2,
    });
    const secondPage = alertsPage({
      alerts: [
        apiAlert({
          id: '2',
          severity: 'warning',
          labels: { alertname: 'DiskFull', severity: 'warning' },
          annotations: { summary: 'Root filesystem above 90%' },
        }),
      ],
      nextCursor: '',
      severityCounts: { critical: 2 },
      total: 2,
    });
    fetchMock().mockImplementation((url: string) => {
      const target = String(url);
      if (target.startsWith('/api/v1/alerts')) {
        if (target.includes('cursor=cursor-2')) {
          pageTwoAttempts += 1;
          return pageTwoAttempts === 1
            ? Promise.reject(new TypeError('fetch failed'))
            : Promise.resolve(jsonResponse(secondPage));
        }
        return Promise.resolve(jsonResponse(firstPage));
      }
      return Promise.resolve(jsonResponse(OPEN_CONFIG));
    });
    render(<App />);

    expect(await screen.findByText('HighErrorRate')).toBeInTheDocument();
    expect(await screen.findByText('Connected')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /load more/i }));

    const footError = await screen.findByRole('alert');
    expect(footError).toHaveTextContent(/could not load more alerts/i);
    expect(screen.getByText('HighErrorRate')).toBeInTheDocument();
    expect(screen.getByText(/showing 1 of 2 firing alerts/i)).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /retry next page/i }));

    expect(await screen.findByText('DiskFull')).toBeInTheDocument();
    expect(screen.getByText(/showing 2 of 2 firing alerts/i)).toBeInTheDocument();
    expect(pageTwoAttempts).toBe(2);
  });

  it('filters the loaded rows only and says so', async () => {
    mockApi(
      alertsPage({
        alerts: [
          apiAlert({ id: '1' }),
          apiAlert({
            id: '2',
            severity: 'warning',
            labels: { alertname: 'DiskFull', severity: 'warning' },
            annotations: { summary: 'Root filesystem above 90%' },
          }),
        ],
        severityCounts: { critical: 1, warning: 1 },
        total: 2,
      }),
    );
    render(<App />);

    expect(await screen.findByText('Connected')).toBeInTheDocument();
    const input = await screen.findByRole('textbox', { name: /filter alerts/i });
    fireEvent.change(input, { target: { value: 'disk' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    expect(screen.queryByText('HighErrorRate')).not.toBeInTheDocument();
    expect(screen.getByText('DiskFull')).toBeInTheDocument();
    expect(screen.getByText(/filter matches loaded rows only/i)).toBeInTheDocument();

    fireEvent.keyDown(input, { key: 'Escape' });
    expect(screen.getByText('HighErrorRate')).toBeInTheDocument();
    expect(input).toHaveValue('');
  });

  it('connects the stream from the snapshot cursor and quietly refreshes on alert events', async () => {
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
          annotations: { summary: 'Root filesystem above 90%' },
        }),
      ],
      severityCounts: { critical: 1, warning: 1 },
      total: 2,
      streamCursor: 9,
    });
    let alertFetches = 0;
    fetchMock().mockImplementation((url: string) => {
      const target = String(url);
      if (target.startsWith('/api/v1/alerts')) {
        alertFetches += 1;
        return Promise.resolve(jsonResponse(alertFetches === 1 ? firstPage : refreshedPage));
      }
      return Promise.resolve(jsonResponse(OPEN_CONFIG));
    });
    render(<App />);

    expect(await screen.findByText('HighErrorRate')).toBeInTheDocument();
    expect(await screen.findByText('Connected')).toBeInTheDocument();
    expect(screen.getByText('1 firing')).toBeInTheDocument();
    expect(screen.getByText(/stream: live/)).toBeInTheDocument();
    expect(FakeEventSource.instances).toHaveLength(1);
    expect(FakeEventSource.latest().url).toBe('/api/v1/stream?cursor=7');

    // A burst of stream events coalesces into one quiet first-page refresh.
    act(() => {
      FakeEventSource.latest().emit(
        'alert.created',
        { id: 8, type: 'alert.created', alertId: '2', occurredAt: '2026-08-14T12:00:00Z' },
        '8',
      );
      FakeEventSource.latest().emit(
        'alert.updated',
        { id: 9, type: 'alert.updated', alertId: '2', occurredAt: '2026-08-14T12:00:01Z' },
        '9',
      );
      FakeEventSource.latest().emit(
        'alert.resolved',
        { id: 9, type: 'alert.resolved', alertId: '3', occurredAt: '2026-08-14T12:00:02Z' },
        '9',
      );
    });

    expect(await screen.findByText('DiskFull', {}, { timeout: 3000 })).toBeInTheDocument();
    expect(screen.getByText('HighErrorRate')).toBeInTheDocument();
    expect(screen.getByText('2 firing')).toBeInTheDocument();
    expect(screen.getByText('2 of 2 alerts')).toBeInTheDocument();
    // Quiet refresh: the initial loading panel never reappeared, and the
    // burst produced exactly one extra request for the first page.
    expect(screen.queryByText(/loading firing alerts/i)).not.toBeInTheDocument();
    expect(alertCalls()).toEqual([
      '/api/v1/alerts?limit=100&status=firing',
      '/api/v1/alerts?limit=100&status=firing',
    ]);

    // The snapshot cursor advanced to 9; a drop resumes from there.
    vi.useFakeTimers();
    act(() => {
      FakeEventSource.latest().emitError();
    });
    expect(screen.getByText('Reconnecting')).toBeInTheDocument();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3000);
    });
    await act(async () => {});
    expect(FakeEventSource.instances).toHaveLength(2);
    expect(FakeEventSource.latest().url).toBe('/api/v1/stream?cursor=9');
    expect(screen.getByText('Connected')).toBeInTheDocument();
    vi.useRealTimers();
  });

  it('shows reconnecting while the stream is down', async () => {
    mockApi(alertsPage({ alerts: [apiAlert()], severityCounts: { critical: 1 }, total: 1 }));
    render(<App />);

    expect(await screen.findByText('HighErrorRate')).toBeInTheDocument();
    expect(await screen.findByText('Connected')).toBeInTheDocument();

    act(() => {
      FakeEventSource.latest().emitError();
    });

    expect(await screen.findByText('Reconnecting')).toBeInTheDocument();
    expect(screen.getByText(/stream: reconnecting/)).toBeInTheDocument();
    // Loaded rows stay put while the stream recovers.
    expect(screen.getByText('HighErrorRate')).toBeInTheDocument();
  });

  it('closes the stream on unmount', async () => {
    mockApi(alertsPage({ alerts: [apiAlert()], total: 1 }));
    const { unmount } = render(<App />);

    expect(await screen.findByText('Connected')).toBeInTheDocument();
    const source = FakeEventSource.latest();
    expect(source.closed).toBe(false);

    unmount();
    expect(source.closed).toBe(true);
  });

  it('opens the detail drawer from a row and closes it via Escape with focus restoration', async () => {
    mockApiWithDetail(alertsPage({ alerts: [apiAlert()], total: 1 }));
    render(<App />);

    const row = await screen.findByRole('row', { name: /HighErrorRate/ });
    row.focus();
    fireEvent.keyDown(row, { key: 'Enter' });

    const dialog = await screen.findByRole('dialog', { name: 'HighErrorRate' });
    expect(window.location.pathname).toBe('/alerts/1');
    expect(dialog).toHaveFocus();
    expect(row).toHaveAttribute('aria-selected', 'true');
    expect(within(dialog).getByText('Error rate above 5% for 10m')).toBeInTheDocument();
    expect(fetchMock()).toHaveBeenCalledWith('/api/v1/alerts/1');

    fireEvent.keyDown(document.body, { key: 'Escape' });

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(window.location.pathname).toBe('/');
    expect(row).toHaveFocus();
    expect(row).toHaveAttribute('aria-selected', 'false');
  });

  it('restores the selection from a direct /alerts/{id} URL and follows back/forward', async () => {
    window.history.pushState(null, '', '/alerts/1');
    mockApiWithDetail(alertsPage({ alerts: [apiAlert()], total: 1 }));
    render(<App />);

    // Deep link: the drawer opens without a row click and the row is marked.
    expect(await screen.findByRole('dialog', { name: 'HighErrorRate' })).toBeInTheDocument();
    const row = await screen.findByRole('row', { name: /HighErrorRate/ });
    expect(row).toHaveAttribute('aria-selected', 'true');

    // Back navigation closes the drawer.
    act(() => {
      window.history.replaceState(null, '', '/');
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(row).toHaveAttribute('aria-selected', 'false');

    // Forward navigation restores it.
    act(() => {
      window.history.replaceState(null, '', '/alerts/1');
      window.dispatchEvent(new PopStateEvent('popstate'));
    });
    expect(await screen.findByRole('dialog', { name: 'HighErrorRate' })).toBeInTheDocument();
  });

  it('shows the not-found state when the deep-linked alert does not exist', async () => {
    window.history.pushState(null, '', '/alerts/gone');
    fetchMock().mockImplementation((url: string) => {
      const target = String(url);
      if (target.startsWith('/api/v1/alerts/')) {
        return Promise.resolve(jsonResponse({ error: 'not found' }, 404));
      }
      if (target.startsWith('/api/v1/alerts')) {
        return Promise.resolve(jsonResponse(alertsPage()));
      }
      return Promise.resolve(jsonResponse(OPEN_CONFIG));
    });
    render(<App />);

    const dialog = await screen.findByRole('dialog', { name: 'Alert detail' });
    expect(within(dialog).getByRole('alert')).toHaveTextContent(/alert not found/i);

    fireEvent.click(within(dialog).getByRole('button', { name: /close alert detail/i }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(window.location.pathname).toBe('/');
  });

  it('quietly refreshes the open detail when a stream event targets it', async () => {
    const page = alertsPage({ alerts: [apiAlert()], total: 1, streamCursor: 7 });
    let detailFetches = 0;
    fetchMock().mockImplementation((url: string) => {
      const target = String(url);
      if (target.startsWith('/api/v1/alerts/')) {
        detailFetches += 1;
        return Promise.resolve(jsonResponse(apiDetailResponse()));
      }
      if (target.startsWith('/api/v1/alerts')) {
        return Promise.resolve(jsonResponse(page));
      }
      return Promise.resolve(jsonResponse(OPEN_CONFIG));
    });
    render(<App />);

    const row = await screen.findByRole('row', { name: /HighErrorRate/ });
    fireEvent.click(row);
    expect(await screen.findByRole('dialog', { name: 'HighErrorRate' })).toBeInTheDocument();
    expect(detailFetches).toBe(1);
    await screen.findByText('Connected');

    // An event for the open alert refreshes the detail (and the list).
    act(() => {
      FakeEventSource.latest().emit(
        'alert.updated',
        { id: 8, type: 'alert.updated', alertId: '1', occurredAt: '2026-08-14T12:00:00Z' },
        '8',
      );
    });
    await waitFor(() => expect(detailFetches).toBe(2), { timeout: 3000 });
    // Quiet refresh: the detail stayed mounted; no loading panel appeared.
    expect(screen.getByRole('dialog', { name: 'HighErrorRate' })).toBeInTheDocument();
    expect(screen.queryByText(/loading alert detail/i)).not.toBeInTheDocument();

    // Events for other alerts never touch the open detail.
    act(() => {
      FakeEventSource.latest().emit(
        'alert.updated',
        { id: 9, type: 'alert.updated', alertId: 'zzz', occurredAt: '2026-08-14T12:00:01Z' },
        '9',
      );
    });
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 1000));
    });
    expect(detailFetches).toBe(2);
  });
});
