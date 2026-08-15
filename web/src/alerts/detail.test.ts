import { describe, expect, it, vi } from 'vitest';
import { AlertsApiError } from './api';
import {
  buildAlertDetailUrl,
  fetchAlertDetail,
  historyTypeLabel,
  isAlertNotFound,
  parseAlertDetailResponse,
  safeExternalUrl,
} from './detail';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function apiHistoryEvent(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: 11,
    occurrence: 2,
    type: 'updated',
    sourceStatus: 'firing',
    actor: 'alertmanager',
    message: 'Notification sent to team-core',
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
    labels: {
      alertname: 'HighErrorRate',
      team: 'core',
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
    occurrence: 2,
    rawData: { status: 'firing', labels: { alertname: 'HighErrorRate' } },
    ...overrides,
  };
}

function apiDetailResponse(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    alert: apiAlertDetail(),
    history: [apiHistoryEvent()],
    ...overrides,
  };
}

describe('buildAlertDetailUrl', () => {
  it('targets the detail endpoint and encodes the id', () => {
    expect(buildAlertDetailUrl('42')).toBe('/api/v1/alerts/42');
    expect(buildAlertDetailUrl('a b/c')).toBe('/api/v1/alerts/a%20b%2Fc');
  });
});

describe('historyTypeLabel', () => {
  it('maps known lifecycle types to human labels', () => {
    expect(historyTypeLabel('created')).toBe('Created');
    expect(historyTypeLabel('updated')).toBe('Updated');
    expect(historyTypeLabel('resolved')).toBe('Resolved');
    expect(historyTypeLabel('reopened')).toBe('Reopened');
    expect(historyTypeLabel('imported')).toBe('Imported');
  });

  it('preserves unknown types instead of dropping them', () => {
    expect(historyTypeLabel('silenced')).toBe('silenced');
    expect(historyTypeLabel('')).toBe('Event');
  });
});

describe('safeExternalUrl', () => {
  it('accepts http and https URLs', () => {
    expect(safeExternalUrl('http://prometheus/graph')).toBe('http://prometheus/graph');
    expect(safeExternalUrl('https://alertmanager.example/#/alerts')).toBe(
      'https://alertmanager.example/#/alerts',
    );
  });

  it('rejects unsafe or unparseable values', () => {
    expect(safeExternalUrl('javascript:alert(1)')).toBeNull();
    expect(safeExternalUrl('file:///etc/passwd')).toBeNull();
    expect(safeExternalUrl('not a url')).toBeNull();
    expect(safeExternalUrl('')).toBeNull();
    expect(safeExternalUrl('   ')).toBeNull();
  });
});

describe('fetchAlertDetail', () => {
  it('requests the detail endpoint and maps the response', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse(apiDetailResponse()));

    const result = await fetchAlertDetail('42', fetchImpl);

    expect(fetchImpl).toHaveBeenCalledTimes(1);
    expect(fetchImpl).toHaveBeenCalledWith('/api/v1/alerts/42');
    expect(result).toEqual({
      alert: {
        id: '42',
        fingerprint: 'fp-42',
        source: 'am-eu',
        status: 'firing',
        severity: 'critical',
        severityLabel: 'Critical',
        name: 'HighErrorRate',
        labels: { alertname: 'HighErrorRate', team: 'core', severity: 'critical' },
        annotations: { summary: 'Error rate above 5% for 10m' },
        startsAt: '2026-08-14T10:00:00Z',
        endsAt: null,
        generatorURL: 'http://prometheus/graph',
        externalURL: 'http://alertmanager',
        firstSeen: '2026-08-14T10:00:00Z',
        lastSeen: '2026-08-14T11:00:00Z',
        repeatCount: 3,
        occurrence: 2,
        rawData: { status: 'firing', labels: { alertname: 'HighErrorRate' } },
      },
      history: [
        {
          id: 11,
          occurrence: 2,
          type: 'updated',
          typeLabel: 'Updated',
          sourceStatus: 'firing',
          actor: 'alertmanager',
          message: 'Notification sent to team-core',
          occurredAt: '2026-08-14T11:00:00Z',
        },
      ],
    });
  });

  it('fails with the HTTP status when the response is not ok', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse({ error: 'nope' }, 404));

    try {
      await fetchAlertDetail('42', fetchImpl);
      expect.unreachable('fetchAlertDetail should have thrown');
    } catch (error) {
      expect(error).toBeInstanceOf(AlertsApiError);
      expect((error as AlertsApiError).message).toMatch(/HTTP 404/);
      expect(isAlertNotFound(error)).toBe(true);
    }
  });

  it('wraps network failures and non-JSON responses', async () => {
    const offline = vi.fn().mockRejectedValue(new TypeError('fetch failed'));
    await expect(fetchAlertDetail('42', offline)).rejects.toThrowError(/unable to reach/i);

    const html = vi.fn().mockResolvedValue(new Response('<html>nope</html>', { status: 200 }));
    await expect(fetchAlertDetail('42', html)).rejects.toThrowError(/not valid json/i);
  });
});

describe('isAlertNotFound', () => {
  it('is true only for 404 API errors', () => {
    expect(isAlertNotFound(new AlertsApiError('gone', { status: 404 }))).toBe(true);
    expect(isAlertNotFound(new AlertsApiError('boom', { status: 500 }))).toBe(false);
    expect(isAlertNotFound(new Error('boom'))).toBe(false);
  });
});

describe('parseAlertDetailResponse', () => {
  it('falls back cleanly for null maps and a missing history list', () => {
    const result = parseAlertDetailResponse(
      apiDetailResponse({
        alert: apiAlertDetail({ labels: null, annotations: null, rawData: null }),
        history: null,
      }),
    );

    expect(result.alert.name).toBe('(unnamed alert)');
    expect(result.alert.labels).toEqual({});
    expect(result.alert.annotations).toEqual({});
    expect(result.alert.rawData).toEqual({});
    expect(result.history).toEqual([]);
  });

  it('tolerates missing optional text fields', () => {
    const result = parseAlertDetailResponse(
      apiDetailResponse({
        alert: apiAlertDetail({ fingerprint: undefined, generatorURL: undefined }),
      }),
    );

    expect(result.alert.fingerprint).toBe('');
    expect(result.alert.generatorURL).toBe('');
  });

  it('maps unknown severities to info while preserving the source text', () => {
    const result = parseAlertDetailResponse(
      apiDetailResponse({ alert: apiAlertDetail({ severity: 'page' }) }),
    );

    expect(result.alert.severity).toBe('info');
    expect(result.alert.severityLabel).toBe('page');
  });

  it('keeps resolved timestamps and maps the resolved state', () => {
    const result = parseAlertDetailResponse(
      apiDetailResponse({
        alert: apiAlertDetail({ status: 'resolved', endsAt: '2026-08-14T12:30:00Z' }),
      }),
    );

    expect(result.alert.status).toBe('resolved');
    expect(result.alert.endsAt).toBe('2026-08-14T12:30:00Z');
  });

  it('labels known history types and preserves unknown ones', () => {
    const result = parseAlertDetailResponse(
      apiDetailResponse({
        history: [
          apiHistoryEvent({ id: 1, type: 'created' }),
          apiHistoryEvent({ id: 2, type: 'resolved' }),
          apiHistoryEvent({ id: 3, type: 'reopened' }),
          apiHistoryEvent({ id: 4, type: 'imported' }),
          apiHistoryEvent({ id: 5, type: 'silenced' }),
        ],
      }),
    );

    expect(result.history.map((event) => event.typeLabel)).toEqual([
      'Created',
      'Resolved',
      'Reopened',
      'Imported',
      'silenced',
    ]);
  });

  it('defaults missing history event text fields', () => {
    const result = parseAlertDetailResponse(
      apiDetailResponse({
        history: [
          apiHistoryEvent({ actor: undefined, message: undefined, sourceStatus: undefined }),
        ],
      }),
    );

    expect(result.history[0]?.actor).toBe('');
    expect(result.history[0]?.message).toBe('');
    expect(result.history[0]?.sourceStatus).toBe('');
  });

  it('rejects malformed envelopes and alerts', () => {
    expect(() => parseAlertDetailResponse(null)).toThrowError(/malformed/i);
    expect(() => parseAlertDetailResponse({ alert: null })).toThrowError(/malformed/i);
    expect(() =>
      parseAlertDetailResponse(apiDetailResponse({ alert: apiAlertDetail({ id: 42 }) })),
    ).toThrowError(/alert\.id must be a string/i);
    expect(() =>
      parseAlertDetailResponse(
        apiDetailResponse({ alert: apiAlertDetail({ status: 'silenced' }) }),
      ),
    ).toThrowError(/unsupported status/i);
    expect(() =>
      parseAlertDetailResponse(apiDetailResponse({ alert: apiAlertDetail({ occurrence: '2' }) })),
    ).toThrowError(/occurrence must be a number/i);
    expect(() =>
      parseAlertDetailResponse(
        apiDetailResponse({ alert: apiAlertDetail({ labels: { team: 7 } }) }),
      ),
    ).toThrowError(/labels\.team must be a string/i);
  });

  it('rejects malformed history entries', () => {
    expect(() => parseAlertDetailResponse(apiDetailResponse({ history: 'nope' }))).toThrowError(
      /history must be a list/i,
    );
    expect(() => parseAlertDetailResponse(apiDetailResponse({ history: [null] }))).toThrowError(
      /malformed/i,
    );
    expect(() =>
      parseAlertDetailResponse(
        apiDetailResponse({ history: [apiHistoryEvent({ occurredAt: 11 })] }),
      ),
    ).toThrowError(/occurredAt must be a string/i);
  });
});
