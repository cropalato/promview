import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  formatDuration,
  isSilenceConflict,
  parseSilencePreview,
  parseSilenceResponse,
  previewGroupSilence,
  silenceAlert,
  silenceDurationOptions,
  silenceGroup,
  SilenceError,
} from './silence';

function fetchMock(): ReturnType<typeof vi.fn> {
  return globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
}

function jsonResponse(body: unknown, status = 201): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn());
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('silenceDurationOptions', () => {
  it('drops choices the deployment would refuse', () => {
    const options = silenceDurationOptions(2 * 60 * 60, 4 * 60 * 60);
    expect(options.every((option) => option.seconds <= 4 * 60 * 60)).toBe(true);
    expect(options.some((option) => option.seconds === 24 * 60 * 60)).toBe(false);
  });

  it('always offers the deployment default, even off the fixed list', () => {
    // A dialog that cannot express its own deployment's default is absurd.
    const options = silenceDurationOptions(90 * 60, 24 * 60 * 60);
    expect(options.some((option) => option.seconds === 90 * 60)).toBe(true);
    const seconds = options.map((option) => option.seconds);
    expect([...seconds].sort((a, b) => a - b)).toEqual(seconds);
  });
});

describe('formatDuration', () => {
  it('names windows the way an operator would', () => {
    expect(formatDuration(30 * 60)).toBe('30 minutes');
    expect(formatDuration(60 * 60)).toBe('1 hour');
    expect(formatDuration(4 * 60 * 60)).toBe('4 hours');
    expect(formatDuration(24 * 60 * 60)).toBe('1 day');
    expect(formatDuration(7 * 24 * 60 * 60)).toBe('7 days');
  });
});

describe('silenceAlert', () => {
  it('posts the window and comment and returns the per-target results', async () => {
    fetchMock().mockResolvedValue(
      jsonResponse({
        endsAt: '2026-08-21T16:00:00Z',
        createdBy: 'ada@example.com',
        results: [{ source: 'demo', silenceId: 'abc' }],
      }),
    );

    const result = await silenceAlert('42', { durationSeconds: 7200, comment: 'maintenance' });

    const [url, init] = fetchMock().mock.calls[0] as [string, RequestInit];
    expect(url).toBe('/api/v1/alerts/42/silence');
    expect(init.method).toBe('POST');
    expect(JSON.parse(String(init.body))).toEqual({
      durationSeconds: 7200,
      comment: 'maintenance',
    });
    expect(result.results).toEqual([
      { source: 'demo', silenceId: 'abc', matchers: {}, members: 0, error: undefined },
    ]);
  });

  it('surfaces the server error rather than a bare status', async () => {
    fetchMock().mockResolvedValue(
      jsonResponse({ error: 'no source in scope has an alertmanager url configured' }, 409),
    );
    await expect(silenceAlert('42', { durationSeconds: 7200, comment: '' })).rejects.toThrow(
      /alertmanager url/,
    );
  });

  it('reports an unreachable API rather than hanging', async () => {
    fetchMock().mockRejectedValue(new Error('offline'));
    await expect(silenceAlert('42', { durationSeconds: 7200, comment: '' })).rejects.toBeInstanceOf(
      SilenceError,
    );
  });
});

describe('silenceGroup', () => {
  it('sends the grouping keys and the key values', async () => {
    fetchMock().mockResolvedValue(jsonResponse({ endsAt: '', createdBy: 'ada', results: [] }));

    await silenceGroup(
      ['alertname', 'source'],
      { alertname: 'HighCPU', source: 'demo' },
      {
        durationSeconds: 1800,
        comment: '',
      },
    );

    const [url, init] = fetchMock().mock.calls[0] as [string, RequestInit];
    expect(url).toBe('/api/v1/groups/silence');
    expect(JSON.parse(String(init.body))).toEqual({
      groupBy: ['alertname', 'source'],
      key: { alertname: 'HighCPU', source: 'demo' },
      durationSeconds: 1800,
      comment: '',
    });
  });

  it('treats a partial application as a result, not a failure', async () => {
    // 207: some Alertmanagers took the silence and some did not. The caller
    // needs the per-target detail, so this must not throw.
    fetchMock().mockResolvedValue(
      jsonResponse(
        {
          endsAt: '2026-08-21T16:00:00Z',
          createdBy: 'ada',
          results: [
            { source: 'demo', silenceId: 'abc' },
            { source: 'edge', error: 'HTTP 401' },
          ],
        },
        207,
      ),
    );

    const result = await silenceGroup(
      ['alertname'],
      { alertname: 'HighCPU' },
      {
        durationSeconds: 7200,
        comment: '',
      },
    );
    expect(result.results.filter((entry) => entry.error !== undefined)).toHaveLength(1);
    expect(result.results.filter((entry) => entry.error === undefined)).toHaveLength(1);
  });
});

describe('injected transport', () => {
  it('uses a caller-supplied fetch instead of the browser default', async () => {
    // The desktop shell keeps credentials in its Rust core, out of the webview,
    // so it supplies its own caller rather than inheriting the cookie jar.
    const calls: [string, RequestInit | undefined][] = [];
    const injected = (url: string, init?: RequestInit) => {
      calls.push([url, init]);
      return Promise.resolve(jsonResponse({ endsAt: '', createdBy: 'ada', results: [] }));
    };

    await silenceAlert('42', { durationSeconds: 7200, comment: '' }, injected);
    await silenceGroup(
      ['alertname'],
      { alertname: 'X' },
      { durationSeconds: 7200, comment: '' },
      undefined,
      injected,
    );

    expect(calls.map(([url]) => url)).toEqual([
      '/api/v1/alerts/42/silence',
      '/api/v1/groups/silence',
    ]);
    expect(calls.every(([, init]) => init?.method === 'POST')).toBe(true);
    // The browser's cookie jar is the default's business, not the caller's.
    expect(calls.every(([, init]) => init?.credentials === undefined)).toBe(true);
    expect(fetchMock()).not.toHaveBeenCalled();
  });

  it('falls back to the browser, leaving its credential handling alone', async () => {
    // `same-origin` is fetch's own default; setting it explicitly would only
    // add noise to every call and every assertion about one.
    fetchMock().mockResolvedValue(jsonResponse({ endsAt: '', createdBy: 'ada', results: [] }));
    await silenceAlert('42', { durationSeconds: 7200, comment: '' });
    const [url, init] = fetchMock().mock.calls[0] as [string, RequestInit];
    expect(url).toBe('/api/v1/alerts/42/silence');
    expect(init.method).toBe('POST');
    expect(init.credentials).toBeUndefined();
  });
});

describe('parseSilenceResponse', () => {
  it('survives a malformed payload rather than taking the dialog down', () => {
    expect(parseSilenceResponse(null).results).toEqual([]);
    expect(parseSilenceResponse({ results: 'nope' }).results).toEqual([]);
    expect(parseSilenceResponse({ results: [null, 7, { source: 'demo' }] }).results).toEqual([
      { source: 'demo', silenceId: undefined, matchers: {}, members: 0, error: undefined },
    ]);
  });
});

describe('previewGroupSilence', () => {
  it('asks the server what the silence would actually match', async () => {
    fetchMock().mockResolvedValue(
      jsonResponse(
        {
          matchers: { alertname: 'HighCPU', cluster: 'prod' },
          memberCount: 12,
          targets: [
            {
              source: 'demo',
              matchers: { alertname: 'HighCPU', cluster: 'prod', instance: 'web-01' },
              members: 12,
            },
          ],
        },
        200,
      ),
    );

    const preview = await previewGroupSilence(['alertname'], { alertname: 'HighCPU' });

    const [url, init] = fetchMock().mock.calls[0] as [string, RequestInit];
    expect(url).toBe('/api/v1/groups/silence/preview');
    expect(JSON.parse(String(init.body))).toEqual({
      groupBy: ['alertname'],
      key: { alertname: 'HighCPU' },
    });
    // The key is `alertname` alone; what gets silenced is narrower, and the
    // dialog has to show that rather than the key it started from.
    expect(preview.matchers).toEqual({ alertname: 'HighCPU', cluster: 'prod' });
    expect(preview.memberCount).toBe(12);
    expect(preview.targets[0]?.matchers.instance).toBe('web-01');
  });

  it('survives a malformed preview rather than blocking the dialog', () => {
    expect(parseSilencePreview(null)).toEqual({ matchers: {}, memberCount: 0, targets: [] });
    expect(parseSilencePreview({ matchers: 'nope', targets: 7 }).targets).toEqual([]);
  });
});

describe('a group that moved between the preview and the confirmation', () => {
  it('carries the new match back so the operator can review it', async () => {
    fetchMock().mockResolvedValue(
      jsonResponse({ error: 'the group changed', matchers: { alertname: 'HighCPU' } }, 409),
    );

    // Silencing more than what was read on screen is the failure this guards.
    try {
      await silenceGroup(
        ['alertname'],
        { alertname: 'HighCPU' },
        { durationSeconds: 7200, comment: '' },
        { alertname: 'HighCPU', cluster: 'a' },
      );
      throw new Error('expected a conflict');
    } catch (error) {
      expect(isSilenceConflict(error)).toBe(true);
      expect((error as SilenceError).matchers).toEqual({ alertname: 'HighCPU' });
    }
  });

  it('sends the match it showed so the server can compare', async () => {
    fetchMock().mockResolvedValue(jsonResponse({ endsAt: '', createdBy: 'ada', results: [] }));
    await silenceGroup(
      ['alertname'],
      { alertname: 'HighCPU' },
      { durationSeconds: 7200, comment: '' },
      { alertname: 'HighCPU', cluster: 'a' },
    );
    const [, init] = fetchMock().mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(String(init.body)).expectedMatchers).toEqual({
      alertname: 'HighCPU',
      cluster: 'a',
    });
  });
});
