import { describe, expect, it, vi } from 'vitest';
import {
  ALERTS_URL,
  AlertsApiError,
  buildAlertsUrl,
  fetchAlerts,
  parseAlertsResponse,
} from './api';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function apiAlert(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: '42',
    fingerprint: 'fp-42',
    source: 'am-eu',
    status: 'firing',
    severity: 'critical',
    labels: {
      alertname: 'HighErrorRate',
      team: 'core',
      instance: 'api-1:9090',
      severity: 'critical',
    },
    annotations: { summary: 'Error rate above 5% for 10m' },
    startsAt: '2026-08-14T10:00:00Z',
    endsAt: null,
    generatorURL: 'http://prometheus/graph',
    externalURL: 'http://alertmanager',
    firstSeen: '2026-08-14T10:00:00Z',
    lastSeen: '2026-08-14T11:00:00Z',
    repeatCount: 3,
    ...overrides,
  };
}

function apiResponse(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    alerts: [apiAlert()],
    nextCursor: 'cursor-2',
    severityCounts: { critical: 5, warning: 2 },
    total: 7,
    streamCursor: 7,
    ...overrides,
  };
}

describe('buildAlertsUrl', () => {
  it('targets the alerts endpoint without params by default', () => {
    expect(ALERTS_URL).toBe('/api/v1/alerts');
    expect(buildAlertsUrl()).toBe('/api/v1/alerts');
  });

  it('encodes pagination params', () => {
    expect(buildAlertsUrl({ limit: 100, status: 'firing' })).toBe(
      '/api/v1/alerts?limit=100&status=firing',
    );
    expect(buildAlertsUrl({ limit: 100, status: 'firing', cursor: 'abc_123-xyz' })).toBe(
      '/api/v1/alerts?limit=100&cursor=abc_123-xyz&status=firing',
    );
  });

  it('encodes filter params and skips empty values', () => {
    expect(buildAlertsUrl({ source: 'am-eu', severity: 'critical', team: 'core' })).toBe(
      '/api/v1/alerts?source=am-eu&severity=critical&team=core',
    );
    expect(buildAlertsUrl({ status: 'resolved', cursor: '' })).toBe(
      '/api/v1/alerts?status=resolved',
    );
  });

  it('repeats the match param for every label matcher', () => {
    expect(buildAlertsUrl({ match: ['severity=critical'] })).toBe(
      '/api/v1/alerts?match=severity%3Dcritical',
    );
    expect(
      buildAlertsUrl({
        limit: 100,
        status: 'firing',
        match: ['severity=critical', 'team!=infra'],
      }),
    ).toBe(
      '/api/v1/alerts?limit=100&status=firing&match=severity%3Dcritical&match=team%21%3Dinfra',
    );
    expect(buildAlertsUrl({ match: [] })).toBe('/api/v1/alerts');
    expect(buildAlertsUrl({ match: [''] })).toBe('/api/v1/alerts');
  });

  it('encodes sort and order params in the endpoint vocabulary', () => {
    expect(buildAlertsUrl({ sort: 'severity', order: 'asc' })).toBe(
      '/api/v1/alerts?sort=severity&order=asc',
    );
    // Console fields map to the endpoint's names.
    expect(buildAlertsUrl({ sort: 'state', order: 'asc' })).toBe(
      '/api/v1/alerts?sort=status&order=asc',
    );
    expect(buildAlertsUrl({ sort: 'name', order: 'desc' })).toBe(
      '/api/v1/alerts?sort=alertname&order=desc',
    );
    // Age ascending (youngest first) is startsAt descending.
    expect(buildAlertsUrl({ sort: 'age', order: 'asc' })).toBe(
      '/api/v1/alerts?sort=startsAt&order=desc',
    );
    expect(
      buildAlertsUrl({
        limit: 100,
        status: 'firing',
        match: ['team=core'],
        sort: 'age',
        order: 'desc',
      }),
    ).toBe('/api/v1/alerts?limit=100&status=firing&match=team%3Dcore&sort=startsAt&order=asc');
  });
});

describe('fetchAlerts', () => {
  it('requests the firing page and maps the response', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse(apiResponse()));

    const page = await fetchAlerts({ limit: 100, status: 'firing' }, fetchImpl);

    expect(fetchImpl).toHaveBeenCalledTimes(1);
    expect(fetchImpl).toHaveBeenCalledWith('/api/v1/alerts?limit=100&status=firing');
    expect(page).toEqual({
      alerts: [
        {
          id: '42',
          severity: 'critical',
          severityLabel: 'Critical',
          state: 'firing',
          name: 'HighErrorRate',
          summary: 'Error rate above 5% for 10m',
          team: 'core',
          instance: 'api-1:9090',
          source: 'am-eu',
          startsAt: '2026-08-14T10:00:00Z',
          notes: 0,
          labels: {
            alertname: 'HighErrorRate',
            team: 'core',
            instance: 'api-1:9090',
            severity: 'critical',
          },
          suppressed: false,
          lastSeen: '2026-08-14T11:00:00Z',
        },
      ],
      nextCursor: 'cursor-2',
      severityCounts: { critical: 5, warning: 2 },
      total: 7,
      streamCursor: 7,
    });
  });

  it('fails with the HTTP status when the response is not ok', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse({ error: 'boom' }, 503));

    try {
      await fetchAlerts({}, fetchImpl);
      expect.unreachable('fetchAlerts should have thrown');
    } catch (error) {
      expect(error).toBeInstanceOf(AlertsApiError);
      expect((error as AlertsApiError).message).toMatch(/HTTP 503/);
      expect((error as AlertsApiError).status).toBe(503);
    }
  });

  it('wraps network failures', async () => {
    const fetchImpl = vi.fn().mockRejectedValue(new TypeError('fetch failed'));

    await expect(fetchAlerts({}, fetchImpl)).rejects.toThrowError(/unable to reach/i);
  });

  it('rejects non-JSON responses', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(new Response('<html>nope</html>', { status: 200 }));

    await expect(fetchAlerts({}, fetchImpl)).rejects.toThrowError(/not valid json/i);
  });
});

describe('parseAlertsResponse', () => {
  it('falls back to the description annotation when summary is missing', () => {
    const page = parseAlertsResponse(
      apiResponse({ alerts: [apiAlert({ annotations: { description: 'Full runbook text' } })] }),
    );

    expect(page.alerts[0]?.summary).toBe('Full runbook text');
  });

  it('provides a useful fallback when neither summary nor description exists', () => {
    const page = parseAlertsResponse(apiResponse({ alerts: [apiAlert({ annotations: {} })] }));

    expect(page.alerts[0]?.summary).toMatch(/no summary or description/i);
  });

  it('names alerts from labels.alertname with an explicit fallback', () => {
    const page = parseAlertsResponse(
      apiResponse({ alerts: [apiAlert({ labels: { severity: 'warning' } })] }),
    );

    expect(page.alerts[0]?.name).toBe('(unnamed alert)');
    expect(page.alerts[0]?.team).toBeUndefined();
    expect(page.alerts[0]?.instance).toBeUndefined();
  });

  it('maps unknown severities to info while preserving the source text', () => {
    const page = parseAlertsResponse(
      apiResponse({ alerts: [apiAlert({ severity: 'page', labels: { severity: 'page' } })] }),
    );

    expect(page.alerts[0]?.severity).toBe('info');
    expect(page.alerts[0]?.severityLabel).toBe('page');
  });

  it('normalizes known severities case-insensitively', () => {
    const page = parseAlertsResponse(apiResponse({ alerts: [apiAlert({ severity: 'CRITICAL' })] }));

    expect(page.alerts[0]?.severity).toBe('critical');
    expect(page.alerts[0]?.severityLabel).toBe('Critical');
  });

  it('maps resolved alerts through to the resolved state', () => {
    const page = parseAlertsResponse(
      apiResponse({ alerts: [apiAlert({ status: 'resolved', endsAt: '2026-08-14T11:30:00Z' })] }),
    );

    expect(page.alerts[0]?.state).toBe('resolved');
  });

  it('tolerates null maps the Go server emits for unset collections', () => {
    const page = parseAlertsResponse(
      apiResponse({
        alerts: [apiAlert({ labels: null, annotations: null })],
        severityCounts: null,
      }),
    );

    expect(page.alerts[0]?.name).toBe('(unnamed alert)');
    expect(page.alerts[0]?.summary).toMatch(/no summary or description/i);
    expect(page.severityCounts).toEqual({});
  });

  it('rejects malformed envelopes', () => {
    expect(() => parseAlertsResponse(null)).toThrowError(/malformed/i);
    expect(() => parseAlertsResponse('firing')).toThrowError(/malformed/i);
    expect(() => parseAlertsResponse({ alerts: 'nope', total: 0 })).toThrowError(
      /alerts must be a list/i,
    );
    expect(() => parseAlertsResponse(apiResponse({ total: '7' }))).toThrowError(
      /total must be a number/i,
    );
    expect(() => parseAlertsResponse(apiResponse({ streamCursor: '7' }))).toThrowError(
      /streamCursor must be a number/i,
    );
    expect(() => parseAlertsResponse(apiResponse({ streamCursor: undefined }))).toThrowError(
      /streamCursor must be a number/i,
    );
  });

  it('maps the expired status, which the console concludes rather than the source reporting it', () => {
    const parsed = parseAlertsResponse(apiResponse({ alerts: [apiAlert({ status: 'expired' })] }));
    expect(parsed.alerts[0]?.state).toBe('expired');
  });

  it('rejects malformed alert rows', () => {
    expect(() => parseAlertsResponse(apiResponse({ alerts: [null] }))).toThrowError(/malformed/i);
    expect(() => parseAlertsResponse(apiResponse({ alerts: [apiAlert({ id: 42 })] }))).toThrowError(
      /id must be a string/i,
    );
    expect(() =>
      parseAlertsResponse(apiResponse({ alerts: [apiAlert({ status: 'silenced' })] })),
    ).toThrowError(/unsupported status/i);
    expect(() =>
      parseAlertsResponse(apiResponse({ alerts: [apiAlert({ labels: { team: 7 } })] })),
    ).toThrowError(/labels\.team must be a string/i);
  });
});
