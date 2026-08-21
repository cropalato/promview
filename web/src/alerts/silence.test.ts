import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  formatDuration,
  parseSilenceResponse,
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
    expect(result.results).toEqual([{ source: 'demo', silenceId: 'abc', error: undefined }]);
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

describe('parseSilenceResponse', () => {
  it('survives a malformed payload rather than taking the dialog down', () => {
    expect(parseSilenceResponse(null).results).toEqual([]);
    expect(parseSilenceResponse({ results: 'nope' }).results).toEqual([]);
    expect(parseSilenceResponse({ results: [null, 7, { source: 'demo' }] }).results).toEqual([
      { source: 'demo', silenceId: undefined, error: undefined },
    ]);
  });
});
