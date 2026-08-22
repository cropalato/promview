import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import App from './App';
import { NOTIFICATION_SEEN_KEY } from './notifications/store';
import { PREFERENCES_KEY, defaultPreferences } from './preferences/store';
import { AutoOpenEventSource, FakeEventSource } from './test/fakeEventSource';
import { FakeNotification } from './test/fakeNotification';

const OPEN_CONFIG = { authMode: 'open', productName: 'Promview' };

const OIDC_CONFIG = { authMode: 'oidc', productName: 'Promview' };

const OIDC_PRINCIPAL = {
  subject: 'https://idp.example|user-1',
  email: 'ada@example.com',
  displayName: 'Ada Lovelace',
  roles: ['operator'],
  anonymous: false,
};

/**
 * The opt-in lives with the operator's other preferences now. In open mode
 * there is no user to key them against, so they fall back to this browser —
 * which is what these tests drive.
 */
function seedNotificationsEnabled(enabled = true): void {
  // Grouping is written off explicitly. These tests assert on a flat alert row
  // and their fetch mock serves no grouped page; before the opt-in moved here
  // they happened to inherit grouping-off from a previous test's leftover
  // preferences, which is not something to keep relying on.
  const preferences = defaultPreferences();
  window.localStorage.setItem(
    PREFERENCES_KEY,
    JSON.stringify({
      ...preferences,
      grouping: { ...preferences.grouping, enabled: false },
      notifications: { ...preferences.notifications, enabled },
    }),
  );
}

function storedNotificationsEnabled(): boolean | undefined {
  const raw = window.localStorage.getItem(PREFERENCES_KEY);
  if (raw === null) {
    return undefined;
  }
  return (JSON.parse(raw) as { notifications?: { enabled?: boolean } }).notifications?.enabled;
}

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

function groupsPage(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    groups: [
      {
        key: { alertname: 'Cardinality', source: 'yul' },
        total: 52,
        acknowledged: 0,
        severityCounts: { critical: 1, warning: 51 },
        worstSeverity: 'critical',
        latestLastSeen: '2026-08-18T12:00:00Z',
        earliestStartsAt: '2026-08-18T11:00:00Z',
        sampleAlertId: '42',
      },
    ],
    nextCursor: '',
    severityCounts: { critical: 1, warning: 51 },
    total: 52,
    totalGroups: 1,
    streamCursor: 7,
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

/**
 * Routes the fetch mock for OIDC deployments: the given /me response, a
 * healthy logout endpoint, the alerts page, and the OIDC config.
 */
function mockOidcApi(me: Response, page: unknown = alertsPage()): void {
  fetchMock().mockImplementation((url: string) => {
    const target = String(url);
    if (target === '/api/v1/me') {
      return Promise.resolve(me);
    }
    if (target === '/api/v1/preferences') {
      // No stored layout for this user, so the console keeps its local one.
      return Promise.resolve(new Response('{}', { status: 404 }));
    }
    if (target === '/api/v1/auth/logout') {
      return Promise.resolve(new Response(null, { status: 204 }));
    }
    if (target.startsWith('/api/v1/alerts')) {
      return Promise.resolve(jsonResponse(page));
    }
    return Promise.resolve(jsonResponse(OIDC_CONFIG));
  });
}

/** Well-formed stream event payload matching the server schema. */
function streamEventPayload(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: 8,
    type: 'alert.updated',
    alertId: '1',
    occurredAt: '2026-08-14T12:00:00Z',
    severity: 'critical',
    alertName: 'HighErrorRate',
    summary: 'Error rate above 5% for 10m',
    source: 'am-eu',
    team: 'core',
    ...overrides,
  };
}

/** Redacted removal payload matching the server schema: envelope only. */
function removedEventPayload(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: 8,
    type: 'alert.removed',
    alertId: '1',
    occurredAt: '2026-08-14T12:00:00Z',
    ...overrides,
  };
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
  FakeNotification.reset();
  // Notification preference/ledger persist in localStorage; start clean.
  window.localStorage.clear();
  // Most tests here exercise the flat list, so they opt out of grouping
  // explicitly. The default itself is pinned by its own test below, which
  // clears this first.
  window.localStorage.setItem(
    'promview.preferences',
    JSON.stringify({
      columns: [
        { id: 'severity' },
        { id: 'state' },
        { id: 'alert' },
        { id: 'summary' },
        { id: 'team' },
        { id: 'instance' },
        { id: 'source' },
        { id: 'age' },
        { id: 'assignee' },
        { id: 'notes' },
      ],
      density: 'normal',
      grouping: { enabled: false, keys: ['alertname', 'source'] },
    }),
  );
  // Tests navigate via pushState; always start from the list route.
  window.history.replaceState(null, '', '/');
});

afterEach(() => {
  // Unmount before the stubs go away. Testing Library's own cleanup is
  // registered when it is imported, so it runs after this hook; without an
  // explicit unmount here the console is still mounted when EventSource and
  // fetch disappear, and an effect or timer that lands in that window throws
  // "EventSource is not defined" against whichever test happens to be running.
  cleanup();
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

  it('never requests the session in open mode', async () => {
    mockApi();
    render(<App />);

    expect(await screen.findByText('Connected')).toBeInTheDocument();
    expect(fetchMock().mock.calls.map(([url]) => String(url))).toEqual([
      '/api/v1/config',
      '/api/v1/alerts?limit=100&status=firing',
    ]);
  });

  it('gates oidc deployments behind a sign-in link when there is no session', async () => {
    mockOidcApi(jsonResponse({ error: 'authentication required' }, 401));
    render(<App />);

    const gate = await screen.findByRole('region', { name: /sign in required/i });
    expect(gate).toHaveTextContent(/oidc sign-in/i);
    const link = within(gate).getByRole('link', {
      name: /sign in with your identity provider/i,
    });
    expect(link).toHaveAttribute('href', '/api/v1/auth/oidc/login');
    expect(screen.getByRole('banner')).toHaveTextContent('Sign-in pending');

    // No alert fetch or stream starts while unauthenticated.
    expect(fetchMock().mock.calls.map(([url]) => String(url))).toEqual([
      '/api/v1/config',
      '/api/v1/me',
    ]);
    expect(alertCalls()).toEqual([]);
    expect(FakeEventSource.instances).toHaveLength(0);
  });

  it('boots the console for an authenticated oidc session', async () => {
    mockOidcApi(
      jsonResponse(OIDC_PRINCIPAL),
      alertsPage({ alerts: [apiAlert()], severityCounts: { critical: 1 }, total: 1 }),
    );
    render(<App />);

    const banner = screen.getByRole('banner');
    expect(await within(banner).findByText('Ada Lovelace')).toBeInTheDocument();
    expect(within(banner).getByText('operator')).toBeInTheDocument();
    expect(within(banner).getByRole('button', { name: /sign out/i })).toBeInTheDocument();

    expect(await screen.findByText('HighErrorRate')).toBeInTheDocument();
    expect(await screen.findByText('Connected')).toBeInTheDocument();

    // The session check ran before the first alert page, which then opened the stream.
    const requested = fetchMock().mock.calls.map(([url]) => String(url));
    expect(requested.filter((url) => url !== '/api/v1/preferences')).toEqual([
      '/api/v1/config',
      '/api/v1/me',
      '/api/v1/alerts?limit=100&status=firing',
    ]);
    // Layout preferences key on the user, so they are fetched once the console
    // unlocks — and only in deployments that have a user to key against.
    expect(requested).toContain('/api/v1/preferences');
    expect(FakeEventSource.latest().url).toBe('/api/v1/stream?cursor=7');
  });

  it('denies read access clearly when the session has no role', async () => {
    mockOidcApi(jsonResponse({ error: 'read access denied' }, 403));
    render(<App />);

    const gate = await screen.findByRole('alert');
    expect(gate).toHaveTextContent(/no read access/i);
    expect(gate).toHaveTextContent(/viewer, operator, or administrator/i);
    expect(within(gate).getByRole('button', { name: /sign out/i })).toBeInTheDocument();
    expect(alertCalls()).toEqual([]);
    expect(FakeEventSource.instances).toHaveLength(0);
  });

  it('shows session errors and retries the session check', async () => {
    let meAttempts = 0;
    fetchMock().mockImplementation((url: string) => {
      const target = String(url);
      if (target === '/api/v1/me') {
        meAttempts += 1;
        return meAttempts === 1
          ? Promise.reject(new TypeError('fetch failed'))
          : Promise.resolve(jsonResponse(OIDC_PRINCIPAL));
      }
      if (target === '/api/v1/preferences') {
        return Promise.resolve(new Response('{}', { status: 404 }));
      }
      if (target.startsWith('/api/v1/alerts')) {
        return Promise.resolve(jsonResponse(alertsPage()));
      }
      return Promise.resolve(jsonResponse(OIDC_CONFIG));
    });
    render(<App />);

    const gate = await screen.findByRole('alert');
    expect(gate).toHaveTextContent(/cannot verify your session/i);
    expect(gate).toHaveTextContent(/unable to reach the promview api/i);
    expect(alertCalls()).toEqual([]);

    fireEvent.click(screen.getByRole('button', { name: /retry session check/i }));

    const banner = screen.getByRole('banner');
    expect(await within(banner).findByText('Ada Lovelace')).toBeInTheDocument();
    expect(await screen.findByRole('heading', { name: /all clear/i })).toBeInTheDocument();
    expect(meAttempts).toBe(2);
    // Unlocking the console starts the stream; wait for it so the connect
    // happens under the test's stubs rather than during teardown.
    expect(await screen.findByText('Connected')).toBeInTheDocument();
  });

  it('signs out through the logout endpoint and navigates home', async () => {
    mockOidcApi(jsonResponse(OIDC_PRINCIPAL));
    const navigate = vi.fn();
    render(<App navigate={navigate} />);

    fireEvent.click(await screen.findByRole('button', { name: /sign out/i }));

    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/'));
    expect(fetchMock()).toHaveBeenCalledWith('/api/v1/auth/logout', { method: 'POST' });
  });

  it('keeps the session and shows an error when sign-out fails', async () => {
    fetchMock().mockImplementation((url: string) => {
      const target = String(url);
      if (target === '/api/v1/auth/logout') {
        return Promise.resolve(jsonResponse({ error: 'boom' }, 500));
      }
      if (target === '/api/v1/me') {
        return Promise.resolve(jsonResponse(OIDC_PRINCIPAL));
      }
      if (target.startsWith('/api/v1/alerts')) {
        return Promise.resolve(jsonResponse(alertsPage()));
      }
      return Promise.resolve(jsonResponse(OIDC_CONFIG));
    });
    const navigate = vi.fn();
    render(<App navigate={navigate} />);

    fireEvent.click(await screen.findByRole('button', { name: /sign out/i }));

    const notice = await screen.findByRole('alert');
    expect(notice).toHaveTextContent(/sign-out failed/i);
    expect(navigate).not.toHaveBeenCalled();
    expect(screen.getByRole('banner')).toHaveTextContent('Ada Lovelace');
  });

  it('returns to the sign-in gate when the first alert page is rejected with 401', async () => {
    fetchMock().mockImplementation((url: string) => {
      const target = String(url);
      if (target === '/api/v1/me') {
        return Promise.resolve(jsonResponse(OIDC_PRINCIPAL));
      }
      if (target.startsWith('/api/v1/alerts')) {
        return Promise.resolve(jsonResponse({ error: 'authentication required' }, 401));
      }
      return Promise.resolve(jsonResponse(OIDC_CONFIG));
    });
    render(<App />);

    // The session verified, then the alerts request exposed the expiry: the
    // console gates instead of showing a generic alerts error.
    const gate = await screen.findByRole('region', { name: /sign in required/i });
    expect(within(gate).getByRole('link', { name: /sign in/i })).toHaveAttribute(
      'href',
      '/api/v1/auth/oidc/login',
    );
    expect(screen.getByRole('banner')).toHaveTextContent('Sign-in pending');
    expect(screen.queryByText('Ada Lovelace')).not.toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(FakeEventSource.instances).toHaveLength(0);
  });

  it('returns to the sign-in gate when the session expires mid-stream', async () => {
    let alertFetches = 0;
    fetchMock().mockImplementation((url: string) => {
      const target = String(url);
      if (target === '/api/v1/me') {
        return Promise.resolve(jsonResponse(OIDC_PRINCIPAL));
      }
      if (target === '/api/v1/preferences') {
        return Promise.resolve(new Response('{}', { status: 404 }));
      }
      if (target.startsWith('/api/v1/alerts')) {
        alertFetches += 1;
        return Promise.resolve(
          alertFetches === 1
            ? jsonResponse(
                alertsPage({ alerts: [apiAlert()], severityCounts: { critical: 1 }, total: 1 }),
              )
            : jsonResponse({ error: 'authentication required' }, 401),
        );
      }
      return Promise.resolve(jsonResponse(OIDC_CONFIG));
    });
    render(<App />);

    // Authenticated console is up and streaming.
    expect(await screen.findByText('HighErrorRate')).toBeInTheDocument();
    expect(await screen.findByText('Connected')).toBeInTheDocument();
    expect(screen.getByRole('banner')).toHaveTextContent('Ada Lovelace');
    const source = FakeEventSource.latest();
    expect(source.closed).toBe(false);

    // The session expires server-side; the stream event triggers a quiet
    // refresh that comes back 401.
    act(() => {
      source.emit('alert.updated', streamEventPayload(), '8');
    });

    const gate = await screen.findByRole(
      'region',
      { name: /sign in required/i },
      { timeout: 3000 },
    );
    expect(within(gate).getByRole('link', { name: /sign in/i })).toHaveAttribute(
      'href',
      '/api/v1/auth/oidc/login',
    );
    expect(screen.getByRole('banner')).toHaveTextContent('Sign-in pending');
    expect(screen.queryByText('Ada Lovelace')).not.toBeInTheDocument();

    // Alert/SSE activity stopped: the stream is closed and no further alert
    // requests fire while gated.
    await waitFor(() => expect(source.closed).toBe(true));
    expect(alertCalls()).toHaveLength(2);
    act(() => {
      source.emit(
        'alert.updated',
        streamEventPayload({ id: 9, occurredAt: '2026-08-14T12:00:01Z' }),
        '9',
      );
    });
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 1000));
    });
    expect(alertCalls()).toHaveLength(2);
  });

  it('renders the columns the operator saved, including one bound to a label', async () => {
    // The layout is browser-local in open mode, which is what this deployment
    // falls back to when there is no user to key preferences against.
    window.localStorage.setItem(
      'promview.preferences',
      JSON.stringify({
        columns: [{ id: 'severity' }, { id: 'alert' }, { id: 'label:prometheus_cluster' }],
        density: 'compact',
        grouping: { enabled: false, keys: ['alertname', 'source'] },
      }),
    );
    mockApi(
      alertsPage({
        alerts: [apiAlert({ labels: { alertname: 'HighErrorRate', prometheus_cluster: 'yul' } })],
        severityCounts: { critical: 1 },
        total: 1,
      }),
    );
    render(<App />);

    expect(await screen.findByText('HighErrorRate')).toBeInTheDocument();
    const headers = screen.getAllByRole('columnheader').map((header) => header.textContent?.trim());
    expect(headers).toEqual(['Severity', 'Alert', 'prometheus_cluster']);
    // The label column renders the alert's own label value.
    expect(screen.getByText('yul')).toBeInTheDocument();
    // Density is applied to the shell so the table can scale with it.
    expect(document.querySelector('[data-density="compact"]')).not.toBeNull();
  });

  it('collapses a fan-out into one group and expands it into members', async () => {
    window.localStorage.setItem(
      'promview.preferences',
      JSON.stringify({
        columns: [{ id: 'severity' }, { id: 'alert' }, { id: 'instance' }, { id: 'age' }],
        density: 'normal',
        grouping: { enabled: true, keys: ['alertname', 'source'] },
      }),
    );
    fetchMock().mockImplementation((url: string) => {
      const target = String(url);
      if (target.includes('groupBy=')) {
        return Promise.resolve(jsonResponse(groupsPage()));
      }
      if (target.startsWith('/api/v1/alerts')) {
        // The members request is the ordinary alerts query plus matchers on the
        // group's key, which is what makes expanding reuse one code path.
        return Promise.resolve(
          jsonResponse(
            alertsPage({
              alerts: [
                apiAlert({ id: '1', labels: { alertname: 'Cardinality', instance: 'a' } }),
                apiAlert({ id: '2', labels: { alertname: 'Cardinality', instance: 'b' } }),
              ],
              total: 52,
              nextCursor: 'more',
            }),
          ),
        );
      }
      return Promise.resolve(jsonResponse(OPEN_CONFIG));
    });
    render(<App />);

    const groupRow = await screen.findByRole('row', { name: /Cardinality/ });
    expect(within(groupRow).getByText('52')).toBeInTheDocument();
    expect(screen.queryByRole('row', { name: /firing/ })).not.toBeInTheDocument();

    fireEvent.click(groupRow);

    expect(await screen.findByText('a')).toBeInTheDocument();
    expect(screen.getByText('b')).toBeInTheDocument();
    const memberRequests = alertCalls().filter((url) => !url.includes('groupBy='));
    expect(memberRequests.some((url) => url.includes('match=alertname%3DCardinality'))).toBe(true);
    expect(memberRequests.some((url) => url.includes('source=yul'))).toBe(true);

    // Collapsing puts the console back to one row.
    fireEvent.click(groupRow);
    expect(screen.queryByText('a')).not.toBeInTheDocument();
  });

  it('keeps expanded groups open across a live refresh', async () => {
    window.localStorage.setItem(
      'promview.preferences',
      JSON.stringify({
        columns: [{ id: 'severity' }, { id: 'alert' }, { id: 'instance' }, { id: 'age' }],
        density: 'normal',
        grouping: { enabled: true, keys: ['alertname', 'source'] },
      }),
    );
    fetchMock().mockImplementation((url: string) => {
      const target = String(url);
      if (target.includes('groupBy=')) {
        return Promise.resolve(jsonResponse(groupsPage()));
      }
      if (target.startsWith('/api/v1/alerts')) {
        return Promise.resolve(
          jsonResponse(
            alertsPage({
              alerts: [
                apiAlert({ id: '1', labels: { alertname: 'Cardinality', instance: 'a' } }),
                apiAlert({ id: '2', labels: { alertname: 'Cardinality', instance: 'b' } }),
              ],
              total: 2,
            }),
          ),
        );
      }
      return Promise.resolve(jsonResponse(OPEN_CONFIG));
    });
    render(<App />);

    const groupRow = await screen.findByRole('row', { name: /Cardinality/ });
    fireEvent.click(groupRow);
    expect(await screen.findByText('a')).toBeInTheDocument();
    expect(await screen.findByText('Connected')).toBeInTheDocument();

    // A stream event re-reads groups and members quietly: the expanded group
    // must not collapse or fall back to a loading panel.
    act(() => {
      FakeEventSource.latest().emit('alert.updated', streamEventPayload(), '8');
    });

    await waitFor(() =>
      expect(
        alertCalls().filter((url) => url.includes('match=alertname%3DCardinality')).length,
      ).toBeGreaterThan(1),
    );
    expect(screen.queryByText(/loading groups/i)).not.toBeInTheDocument();
    expect(groupRow).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByText('a')).toBeInTheDocument();
    expect(screen.getByText('b')).toBeInTheDocument();
  });

  it('switches between grouped and flat from the view menu, and remembers it', async () => {
    // No stored layout: this is the out-of-the-box console.
    window.localStorage.clear();
    fetchMock().mockImplementation((url: string) => {
      const target = String(url);
      if (target.includes('groupBy=')) {
        return Promise.resolve(jsonResponse(groupsPage()));
      }
      if (target.startsWith('/api/v1/alerts')) {
        return Promise.resolve(
          jsonResponse(
            alertsPage({ alerts: [apiAlert()], total: 1, severityCounts: { critical: 1 } }),
          ),
        );
      }
      return Promise.resolve(jsonResponse(OPEN_CONFIG));
    });
    render(<App />);

    // Grouping is on by default: the fan-out it collapses is what makes a busy
    // console unreadable.
    expect(await screen.findByRole('treegrid')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'View' }));
    fireEvent.click(screen.getByRole('checkbox', { name: /group alerts/i }));

    expect(await screen.findByText('HighErrorRate')).toBeInTheDocument();
    expect(screen.queryByRole('treegrid')).not.toBeInTheDocument();
    // The choice is written through to preferences so it survives a reload.
    const stored = JSON.parse(window.localStorage.getItem('promview.preferences') ?? '{}');
    expect(stored.grouping.enabled).toBe(false);
  });

  it('adds a column bound to a label from the view menu', async () => {
    mockApi(
      alertsPage({
        alerts: [apiAlert({ labels: { alertname: 'HighErrorRate', prometheus_cluster: 'yul' } })],
        total: 1,
      }),
    );
    window.localStorage.setItem(
      'promview.preferences',
      JSON.stringify({
        columns: [{ id: 'severity' }, { id: 'alert' }],
        density: 'normal',
        grouping: { enabled: false, keys: ['alertname', 'source'] },
      }),
    );
    render(<App />);

    expect(await screen.findByText('HighErrorRate')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'View' }));
    fireEvent.change(screen.getByLabelText('Label name'), {
      target: { value: 'prometheus_cluster' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add' }));

    const headers = screen.getAllByRole('columnheader').map((header) => header.textContent?.trim());
    expect(headers).toEqual(['Severity', 'Alert', 'prometheus_cluster']);
    expect(screen.getByText('yul')).toBeInTheDocument();
  });

  it('picks a row density from the viewport when the operator has not chosen one', async () => {
    // Auto is the default; a short viewport should tighten the rows without the
    // operator configuring anything.
    window.localStorage.clear();
    Object.defineProperty(window, 'innerHeight', {
      value: 600,
      configurable: true,
      writable: true,
    });
    fetchMock().mockImplementation((url: string) => {
      const target = String(url);
      if (target.includes('groupBy=')) {
        return Promise.resolve(jsonResponse(groupsPage()));
      }
      if (target.startsWith('/api/v1/alerts')) {
        return Promise.resolve(jsonResponse(alertsPage()));
      }
      return Promise.resolve(jsonResponse(OPEN_CONFIG));
    });
    render(<App />);

    await screen.findByRole('treegrid');
    expect(document.querySelector('[data-density="compact"]')).not.toBeNull();

    // A stored choice wins over the measurement.
    fireEvent.click(screen.getByRole('button', { name: 'View' }));
    fireEvent.click(screen.getByRole('radio', { name: /comfortable/i }));
    expect(document.querySelector('[data-density="comfortable"]')).not.toBeNull();
    const stored = JSON.parse(window.localStorage.getItem('promview.preferences') ?? '{}');
    expect(stored.density).toBe('comfortable');
  });

  it('pins the chosen palette on the document and stores it', async () => {
    window.localStorage.clear();
    fetchMock().mockImplementation((url: string) => {
      const target = String(url);
      if (target.includes('groupBy=')) {
        return Promise.resolve(jsonResponse(groupsPage()));
      }
      if (target.startsWith('/api/v1/alerts')) {
        return Promise.resolve(jsonResponse(alertsPage()));
      }
      return Promise.resolve(jsonResponse(OPEN_CONFIG));
    });
    render(<App />);

    await screen.findByRole('treegrid');
    // System is the default, and it deliberately sets no attribute so the
    // stylesheet's prefers-color-scheme rule stays in charge.
    expect(document.documentElement.hasAttribute('data-theme')).toBe(false);

    fireEvent.change(screen.getByRole('combobox', { name: /theme/i }), {
      target: { value: 'nord' },
    });
    expect(document.documentElement.dataset.theme).toBe('nord');
    const stored = JSON.parse(window.localStorage.getItem('promview.preferences') ?? '{}');
    expect(stored.theme).toBe('nord');

    // Back to system releases the document again.
    fireEvent.change(screen.getByRole('combobox', { name: /theme/i }), {
      target: { value: 'system' },
    });
    expect(document.documentElement.hasAttribute('data-theme')).toBe(false);
  });

  it('offers group silencing only to an operator on a deployment that can silence', async () => {
    // Open mode hands every reader the same anonymous viewer, so the control
    // could only ever answer 403. A button that always fails is worse than
    // none, and the server refusing is not a substitute for not offering it.
    const respond = (config: unknown, principal?: unknown) => (url: string) => {
      const target = String(url);
      if (target.includes('groupBy=')) {
        return Promise.resolve(jsonResponse(groupsPage()));
      }
      if (target.startsWith('/api/v1/alerts')) {
        return Promise.resolve(jsonResponse(alertsPage()));
      }
      if (target === '/api/v1/me') {
        return Promise.resolve(jsonResponse(principal));
      }
      return Promise.resolve(jsonResponse(config));
    };

    window.localStorage.clear();
    fetchMock().mockImplementation(
      respond({
        ...OPEN_CONFIG,
        silenceEnabled: true,
        silenceDefaultSeconds: 7200,
        silenceMaxSeconds: 86400,
      }),
    );
    const open = render(<App />);
    await screen.findByRole('treegrid');
    expect(screen.queryByRole('button', { name: /^Silence / })).not.toBeInTheDocument();
    open.unmount();

    // A viewer in oidc mode is refused for the same reason.
    fetchMock().mockImplementation(
      respond(
        {
          ...OIDC_CONFIG,
          silenceEnabled: true,
          silenceDefaultSeconds: 7200,
          silenceMaxSeconds: 86400,
        },
        { ...OIDC_PRINCIPAL, roles: ['viewer'] },
      ),
    );
    const viewer = render(<App />);
    await screen.findByRole('treegrid');
    expect(screen.queryByRole('button', { name: /^Silence / })).not.toBeInTheDocument();
    viewer.unmount();

    // An operator on a deployment that can reach an Alertmanager gets it.
    fetchMock().mockImplementation(
      respond(
        {
          ...OIDC_CONFIG,
          silenceEnabled: true,
          silenceDefaultSeconds: 7200,
          silenceMaxSeconds: 86400,
        },
        OIDC_PRINCIPAL,
      ),
    );
    render(<App />);
    await screen.findByRole('treegrid');
    expect(await screen.findByRole('button', { name: /^Silence / })).toBeInTheDocument();
  });

  it('hides group silencing when the deployment cannot reach an Alertmanager', async () => {
    window.localStorage.clear();
    fetchMock().mockImplementation((url: string) => {
      const target = String(url);
      if (target.includes('groupBy=')) {
        return Promise.resolve(jsonResponse(groupsPage()));
      }
      if (target.startsWith('/api/v1/alerts')) {
        return Promise.resolve(jsonResponse(alertsPage()));
      }
      if (target === '/api/v1/me') {
        return Promise.resolve(jsonResponse(OIDC_PRINCIPAL));
      }
      return Promise.resolve(jsonResponse({ ...OIDC_CONFIG, silenceEnabled: false }));
    });
    render(<App />);
    await screen.findByRole('treegrid');
    expect(screen.queryByRole('button', { name: /^Silence / })).not.toBeInTheDocument();
  });

  it('switches the empty state when a filter is applied and cleared', async () => {
    mockApi();
    render(<App />);

    expect(await screen.findByText('Connected')).toBeInTheDocument();
    const input = await screen.findByRole('textbox', { name: /filter alerts/i });
    fireEvent.change(input, { target: { value: 'severity="critical"' } });
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
    // Collect every pending alerts request rather than keeping only the last
    // resolver: the console can ask for more than one shape of the endpoint,
    // and stranding the one it is actually waiting on leaves it loading.
    const pendingAlerts: Array<(response: Response) => void> = [];
    fetchMock().mockImplementation((url: string) => {
      if (String(url).startsWith('/api/v1/alerts')) {
        return new Promise<Response>((resolve) => {
          pendingAlerts.push(resolve);
        });
      }
      return Promise.resolve(jsonResponse(OPEN_CONFIG));
    });
    render(<App />);

    // Shell is configured but live data has not landed yet: still syncing.
    expect(await screen.findByText(/loading firing alerts/i)).toBeInTheDocument();
    expect(screen.getByText('Syncing')).toBeInTheDocument();
    expect(screen.getByRole('heading', { level: 1, name: 'Alerts' })).toBeInTheDocument();

    // The loading panel also shows while the config request is in flight, so
    // reaching it does not prove an alerts request exists yet. Waiting for the
    // request is what makes resolving it deterministic; without this the
    // resolve can land before the request and the console loads forever.
    await waitFor(() => expect(alertCalls()).not.toHaveLength(0));
    for (const resolve of pendingAlerts) {
      resolve(jsonResponse(alertsPage()));
    }
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

  it('applies the label filter server-side with repeated match params', async () => {
    fetchMock().mockImplementation((url: string) => {
      const target = String(url);
      if (target.startsWith('/api/v1/alerts')) {
        const filtered = target.includes('match=');
        return Promise.resolve(
          jsonResponse(
            filtered
              ? alertsPage({
                  alerts: [
                    apiAlert({
                      id: '2',
                      severity: 'warning',
                      labels: { alertname: 'DiskFull', severity: 'warning' },
                      annotations: { summary: 'Root filesystem above 90%' },
                    }),
                  ],
                  severityCounts: { warning: 1 },
                  total: 1,
                })
              : alertsPage({
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
          ),
        );
      }
      return Promise.resolve(jsonResponse(OPEN_CONFIG));
    });
    render(<App />);

    expect(await screen.findByText('Connected')).toBeInTheDocument();
    expect(screen.getByText('HighErrorRate')).toBeInTheDocument();

    const input = await screen.findByRole('textbox', { name: /filter alerts/i });
    fireEvent.change(input, { target: { value: 'alertname="DiskFull"' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    // The server-side result replaces the list; the total follows the filter.
    expect(await screen.findByText('1 of 1 alerts')).toBeInTheDocument();
    expect(screen.queryByText('HighErrorRate')).not.toBeInTheDocument();
    expect(screen.getByText('DiskFull')).toBeInTheDocument();
    expect(alertCalls()).toContain(
      '/api/v1/alerts?limit=100&status=firing&match=alertname%3DDiskFull',
    );

    // Escape clears the draft and the applied filter: the unfiltered first
    // page comes back.
    fireEvent.keyDown(input, { key: 'Escape' });
    expect(await screen.findByText('HighErrorRate')).toBeInTheDocument();
    expect(input).toHaveValue('');
    expect(alertCalls()[alertCalls().length - 1]).toBe('/api/v1/alerts?limit=100&status=firing');
  });

  it('parses brace-wrapped comma-separated filters into one match param each', async () => {
    mockApi();
    render(<App />);

    expect(await screen.findByText('Connected')).toBeInTheDocument();
    const input = await screen.findByRole('textbox', { name: /filter alerts/i });
    fireEvent.change(input, { target: { value: '{severity="critical", team!="infra"}' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => expect(alertCalls()).toHaveLength(2));
    expect(alertCalls()[1]).toBe(
      '/api/v1/alerts?limit=100&status=firing&match=severity%3Dcritical&match=team%21%3Dinfra',
    );
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('shows a parse error and sends no request for an invalid filter', async () => {
    mockApi();
    render(<App />);

    expect(await screen.findByText('Connected')).toBeInTheDocument();
    const input = await screen.findByRole('textbox', { name: /filter alerts/i });
    fireEvent.change(input, { target: { value: 'critical' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    const message = await screen.findByRole('alert');
    expect(message).toHaveTextContent(/expected one of = or !=/);
    expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(input).toHaveValue('critical');
    // Only the initial page was requested.
    expect(alertCalls()).toEqual(['/api/v1/alerts?limit=100&status=firing']);

    // Editing the draft retires the error; a valid draft applies again.
    fireEvent.change(input, { target: { value: 'severity="critical"' } });
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(alertCalls()).toHaveLength(2));
    expect(alertCalls()[1]).toBe(
      '/api/v1/alerts?limit=100&status=firing&match=severity%3Dcritical',
    );
  });

  it('sorts server-side from the table headers', async () => {
    mockApi(alertsPage({ alerts: [apiAlert()], severityCounts: { critical: 1 }, total: 1 }));
    render(<App />);

    expect(await screen.findByText('HighErrorRate')).toBeInTheDocument();

    const severityHeader = () =>
      screen.getByRole('button', { name: 'Sort by Severity' }).closest('th');

    fireEvent.click(screen.getByRole('button', { name: 'Sort by Severity' }));
    await waitFor(() => expect(alertCalls()).toHaveLength(2));
    expect(alertCalls()[1]).toBe('/api/v1/alerts?limit=100&status=firing&sort=severity&order=asc');
    expect(severityHeader()).toHaveAttribute('aria-sort', 'ascending');

    fireEvent.click(screen.getByRole('button', { name: 'Sort by Severity' }));
    await waitFor(() => expect(alertCalls()).toHaveLength(3));
    expect(alertCalls()[2]).toBe('/api/v1/alerts?limit=100&status=firing&sort=severity&order=desc');
    expect(severityHeader()).toHaveAttribute('aria-sort', 'descending');
  });

  it('combines the label filter with the active sort in one request', async () => {
    mockApi();
    render(<App />);

    expect(await screen.findByText('Connected')).toBeInTheDocument();
    const input = await screen.findByRole('textbox', { name: /filter alerts/i });
    fireEvent.change(input, { target: { value: 'team="core"' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(alertCalls()).toHaveLength(2));

    fireEvent.click(screen.getByRole('button', { name: 'Sort by Age' }));
    await waitFor(() => expect(alertCalls()).toHaveLength(3));
    expect(alertCalls()[2]).toBe(
      '/api/v1/alerts?limit=100&status=firing&match=team%3Dcore&sort=startsAt&order=desc',
    );
  });

  it('sorts the grouped view from its column headers', async () => {
    window.localStorage.setItem(
      'promview.preferences',
      JSON.stringify({
        columns: [{ id: 'severity' }, { id: 'alert' }, { id: 'instance' }, { id: 'age' }],
        density: 'normal',
        grouping: { enabled: true, keys: ['alertname', 'source'] },
      }),
    );
    fetchMock().mockImplementation((url: string) => {
      const target = String(url);
      if (target.includes('groupBy=')) {
        return Promise.resolve(jsonResponse(groupsPage()));
      }
      if (target.startsWith('/api/v1/alerts')) {
        return Promise.resolve(jsonResponse(alertsPage()));
      }
      return Promise.resolve(jsonResponse(OPEN_CONFIG));
    });
    render(<App />);

    expect(await screen.findByRole('treegrid')).toBeInTheDocument();
    const groupCalls = () => alertCalls().filter((url) => url.includes('groupBy='));
    expect(groupCalls()).toEqual([
      '/api/v1/alerts?limit=100&status=firing&groupBy=alertname%2Csource',
    ]);

    const ageHeader = () => screen.getByRole('button', { name: 'Sort by Age' }).closest('th');

    // Age ascending reads youngest-first, which the endpoint takes as
    // startsAt descending.
    fireEvent.click(screen.getByRole('button', { name: 'Sort by Age' }));
    await waitFor(() => expect(groupCalls()).toHaveLength(2));
    expect(groupCalls()[1]).toBe(
      '/api/v1/alerts?limit=100&status=firing&sort=startsAt&order=desc&groupBy=alertname%2Csource',
    );
    expect(ageHeader()).toHaveAttribute('aria-sort', 'ascending');

    fireEvent.click(screen.getByRole('button', { name: 'Sort by Age' }));
    await waitFor(() => expect(groupCalls()).toHaveLength(3));
    expect(groupCalls()[2]).toBe(
      '/api/v1/alerts?limit=100&status=firing&sort=startsAt&order=asc&groupBy=alertname%2Csource',
    );
    expect(ageHeader()).toHaveAttribute('aria-sort', 'descending');
  });

  it('applies and upserts matchers from the detail drawer label buttons', async () => {
    mockApiWithDetail(alertsPage({ alerts: [apiAlert()], total: 1 }));
    render(<App />);

    const row = await screen.findByRole('row', { name: /HighErrorRate/ });
    fireEvent.click(row);
    const dialog = await screen.findByRole('dialog', { name: 'HighErrorRate' });

    // Include: team="core" is applied and reflected into the filter input.
    fireEvent.click(within(dialog).getByRole('button', { name: 'Filter to team="core"' }));
    const input = screen.getByRole('textbox', { name: /filter alerts/i });
    expect(input).toHaveValue('team="core"');
    await waitFor(() =>
      expect(alertCalls()).toContain('/api/v1/alerts?limit=100&status=firing&match=team%3Dcore'),
    );

    // Exclude upserts the same label in place instead of adding a second
    // matcher for it.
    fireEvent.click(within(dialog).getByRole('button', { name: 'Exclude team="core"' }));
    expect(input).toHaveValue('team!="core"');
    await waitFor(() =>
      expect(alertCalls()).toContain('/api/v1/alerts?limit=100&status=firing&match=team%21%3Dcore'),
    );

    // A second label wraps the expression in braces.
    fireEvent.click(
      within(dialog).getByRole('button', { name: 'Filter to instance="api-1:9090"' }),
    );
    expect(input).toHaveValue('{team!="core", instance="api-1:9090"}');
    await waitFor(() =>
      expect(alertCalls()).toContain(
        '/api/v1/alerts?limit=100&status=firing&match=team%21%3Dcore&match=instance%3Dapi-1%3A9090',
      ),
    );
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

    // A burst of stream events — including a redacted removal — coalesces
    // into one quiet first-page refresh.
    act(() => {
      FakeEventSource.latest().emit(
        'alert.created',
        streamEventPayload({ type: 'alert.created', alertId: '2' }),
        '8',
      );
      FakeEventSource.latest().emit(
        'alert.updated',
        streamEventPayload({ id: 9, alertId: '2', occurredAt: '2026-08-14T12:00:01Z' }),
        '9',
      );
      FakeEventSource.latest().emit(
        'alert.resolved',
        streamEventPayload({
          id: 9,
          type: 'alert.resolved',
          alertId: '3',
          occurredAt: '2026-08-14T12:00:02Z',
        }),
        '9',
      );
      FakeEventSource.latest().emit(
        'alert.removed',
        removedEventPayload({ id: 9, alertId: '3', occurredAt: '2026-08-14T12:00:03Z' }),
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
    expect(await within(dialog).findByRole('alert')).toHaveTextContent(/alert not found/i);

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
      FakeEventSource.latest().emit('alert.updated', streamEventPayload(), '8');
    });
    await waitFor(() => expect(detailFetches).toBe(2), { timeout: 3000 });
    // Quiet refresh: the detail stayed mounted; no loading panel appeared.
    expect(screen.getByRole('dialog', { name: 'HighErrorRate' })).toBeInTheDocument();
    expect(screen.queryByText(/loading alert detail/i)).not.toBeInTheDocument();

    // Events for other alerts never touch the open detail.
    act(() => {
      FakeEventSource.latest().emit(
        'alert.updated',
        streamEventPayload({ id: 9, alertId: 'zzz', occurredAt: '2026-08-14T12:00:01Z' }),
        '9',
      );
    });
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 1000));
    });
    expect(detailFetches).toBe(2);

    // A redacted removal targeting the open alert refreshes it like any
    // other event.
    act(() => {
      FakeEventSource.latest().emit(
        'alert.removed',
        removedEventPayload({ id: 10, occurredAt: '2026-08-14T12:00:02Z' }),
        '10',
      );
    });
    await waitFor(() => expect(detailFetches).toBe(3), { timeout: 3000 });
    expect(screen.getByRole('dialog', { name: 'HighErrorRate' })).toBeInTheDocument();
    expect(screen.queryByText(/loading alert detail/i)).not.toBeInTheDocument();
  });
});

describe('App browser notifications', () => {
  it('shows an inert opt-in control when the browser has no Notification API', async () => {
    mockApi();
    render(<App />);

    await screen.findByText('Connected');
    const toggle = screen.getByRole('button', { name: /does not support notifications/i });
    expect(toggle).toBeDisabled();
  });

  it('opts in on click, persists the preference, and never prompts beforehand', async () => {
    vi.stubGlobal('Notification', FakeNotification);
    mockApi();
    render(<App />);

    await screen.findByText('Connected');
    const toggle = await screen.findByRole('button', {
      name: /enable critical alert notifications/i,
    });
    // Permission was already granted, so enabling needs no prompt.
    expect(FakeNotification.requestCount).toBe(0);

    fireEvent.click(toggle);

    expect(
      await screen.findByRole('button', { name: /mute critical alert notifications/i }),
    ).toHaveAttribute('aria-pressed', 'true');
    expect(FakeNotification.requestCount).toBe(0);
    expect(storedNotificationsEnabled()).toBe(true);

    fireEvent.click(screen.getByRole('button', { name: /mute critical alert notifications/i }));
    expect(storedNotificationsEnabled()).toBe(false);
  });

  it('prompts for permission only from the opt-in click', async () => {
    FakeNotification.permission = 'default';
    FakeNotification.nextRequestResult = 'granted';
    vi.stubGlobal('Notification', FakeNotification);
    mockApi();
    render(<App />);

    await screen.findByText('Connected');
    // Mounting the console must never trigger the browser prompt.
    expect(FakeNotification.requestCount).toBe(0);

    fireEvent.click(screen.getByRole('button', { name: /enable critical alert notifications/i }));

    expect(
      await screen.findByRole('button', { name: /mute critical alert notifications/i }),
    ).toBeInTheDocument();
    expect(FakeNotification.requestCount).toBe(1);
    expect(storedNotificationsEnabled()).toBe(true);
  });

  it('reflects a denied browser permission without prompting', async () => {
    FakeNotification.permission = 'denied';
    vi.stubGlobal('Notification', FakeNotification);
    mockApi();
    render(<App />);

    await screen.findByText('Connected');
    const toggle = await screen.findByRole('button', { name: /blocked in the browser settings/i });
    expect(toggle).toBeDisabled();
    fireEvent.click(toggle);
    expect(FakeNotification.requestCount).toBe(0);
    expect(storedNotificationsEnabled() ?? false).toBe(false);
  });

  it('notifies a new critical alert while hidden; the click focuses and deep-links', async () => {
    seedNotificationsEnabled();
    vi.stubGlobal('Notification', FakeNotification);
    const focus = vi.fn();
    vi.stubGlobal('focus', focus);
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(true);
    mockApiWithDetail(alertsPage({ alerts: [apiAlert()], total: 1, streamCursor: 7 }));
    render(<App />);

    expect(await screen.findByText('HighErrorRate')).toBeInTheDocument();
    await screen.findByText('Connected');

    act(() => {
      FakeEventSource.latest().emit(
        'alert.created',
        streamEventPayload({ type: 'alert.created', alertId: '2' }),
        '8',
      );
    });

    expect(FakeNotification.instances).toHaveLength(1);
    const notification = FakeNotification.latest();
    expect(notification.title).toBe('Critical: HighErrorRate');
    expect(notification.body).toBe('Error rate above 5% for 10m\nam-eu · core');

    act(() => {
      notification.click();
    });

    expect(focus).toHaveBeenCalledTimes(1);
    expect(window.location.pathname).toBe('/alerts/2');
    expect(await screen.findByRole('dialog')).toBeInTheDocument();
    expect(notification.closed).toBe(true);
  });

  it('suppresses while the tab is visible and dedupes the replay once hidden', async () => {
    seedNotificationsEnabled();
    vi.stubGlobal('Notification', FakeNotification);
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(false);
    mockApi(alertsPage({ streamCursor: 7 }));
    render(<App />);

    await screen.findByText('Connected');

    // Visible tab: the new critical alert is shown in the list instead, but
    // its event id is still recorded so a replay can never notify late.
    act(() => {
      FakeEventSource.latest().emit(
        'alert.created',
        streamEventPayload({ type: 'alert.created', alertId: '2' }),
        '8',
      );
    });
    expect(FakeNotification.instances).toHaveLength(0);
    expect(window.localStorage.getItem(NOTIFICATION_SEEN_KEY)).toContain('8');

    // The tab is hidden later; a replayed id 8 stays silent, a new id fires.
    hidden.mockReturnValue(true);
    act(() => {
      FakeEventSource.latest().emit(
        'alert.created',
        streamEventPayload({ type: 'alert.created', alertId: '2' }),
        '8',
      );
      FakeEventSource.latest().emit(
        'alert.created',
        streamEventPayload({ id: 9, type: 'alert.created', alertId: '2' }),
        '9',
      );
    });
    expect(FakeNotification.instances).toHaveLength(1);
  });

  it('stays silent for non-critical or non-created events even when hidden', async () => {
    seedNotificationsEnabled();
    vi.stubGlobal('Notification', FakeNotification);
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(true);
    mockApi(alertsPage({ streamCursor: 7 }));
    render(<App />);

    await screen.findByText('Connected');

    act(() => {
      FakeEventSource.latest().emit(
        'alert.created',
        streamEventPayload({ type: 'alert.created', alertId: '2', severity: 'warning' }),
        '8',
      );
      FakeEventSource.latest().emit(
        'alert.updated',
        streamEventPayload({ id: 9, alertId: '2' }),
        '9',
      );
      // Redacted removals never notify, even opted in and hidden.
      FakeEventSource.latest().emit(
        'alert.removed',
        removedEventPayload({ id: 10, alertId: '2' }),
        '10',
      );
    });

    expect(FakeNotification.instances).toHaveLength(0);
  });

  it('stays silent while opted out, and old events never notify after opting in', async () => {
    vi.stubGlobal('Notification', FakeNotification);
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(true);
    mockApi(alertsPage({ streamCursor: 7 }));
    render(<App />);

    await screen.findByText('Connected');

    // Opted out: the critical event passes by silently but is recorded.
    act(() => {
      FakeEventSource.latest().emit(
        'alert.created',
        streamEventPayload({ type: 'alert.created', alertId: '2' }),
        '8',
      );
    });
    expect(FakeNotification.instances).toHaveLength(0);

    fireEvent.click(
      await screen.findByRole('button', { name: /enable critical alert notifications/i }),
    );
    await screen.findByRole('button', { name: /mute critical alert notifications/i });

    // A replay of the pre-opt-in event must not notify; only new events do.
    act(() => {
      FakeEventSource.latest().emit(
        'alert.created',
        streamEventPayload({ type: 'alert.created', alertId: '2' }),
        '8',
      );
      FakeEventSource.latest().emit(
        'alert.created',
        streamEventPayload({ id: 9, type: 'alert.created', alertId: '3' }),
        '9',
      );
    });
    expect(FakeNotification.instances).toHaveLength(1);
    expect(FakeNotification.latest().tag).toBe('promview-alert-3');
  });
});
