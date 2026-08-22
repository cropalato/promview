import { afterEach, describe, expect, it, vi } from 'vitest';
import { apiBaseUrl, apiUrl, setApiBaseUrl } from './apiBase';
import { loadRuntimeConfig } from './runtimeConfig';
import { SESSION_URL, loadSession } from '../auth/session';
import { silenceAlert } from '../alerts/silence';

afterEach(() => {
  // Module state: every test starts from the browser's same-origin default.
  setApiBaseUrl('');
});

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

describe('apiUrl', () => {
  it('leaves paths relative for the browser, which talks to its own origin', () => {
    expect(apiBaseUrl()).toBe('');
    expect(apiUrl('/api/v1/alerts')).toBe('/api/v1/alerts');
  });

  it('resolves against a configured server', () => {
    setApiBaseUrl('https://promview.example');
    expect(apiUrl('/api/v1/alerts')).toBe('https://promview.example/api/v1/alerts');
  });

  it('does not double the slash when the base carries one', () => {
    setApiBaseUrl('https://promview.example/');
    expect(apiUrl('/api/v1/alerts')).toBe('https://promview.example/api/v1/alerts');
  });

  it('keeps a path prefix, for a server behind a reverse proxy subpath', () => {
    setApiBaseUrl('https://ops.example/promview/');
    expect(apiUrl('/api/v1/alerts')).toBe('https://ops.example/promview/api/v1/alerts');
  });

  it('preserves the query a path already carries', () => {
    setApiBaseUrl('https://promview.example');
    expect(apiUrl('/api/v1/stream?cursor=42')).toBe(
      'https://promview.example/api/v1/stream?cursor=42',
    );
  });

  it('returns to relative paths when cleared', () => {
    setApiBaseUrl('https://promview.example');
    setApiBaseUrl('');
    expect(apiUrl('/api/v1/alerts')).toBe('/api/v1/alerts');
  });
});

describe('setApiBaseUrl', () => {
  it('refuses a base that would misroute requests', () => {
    // A relative base cannot be joined predictably, and a query or fragment
    // would be silently dropped by path joining.
    expect(() => setApiBaseUrl('promview.example')).toThrow(/absolute/);
    expect(() => setApiBaseUrl('/promview')).toThrow(/absolute/);
    expect(() => setApiBaseUrl('ftp://promview.example')).toThrow(/http/);
    expect(() => setApiBaseUrl('https://promview.example?token=x')).toThrow(/query or fragment/);
    expect(() => setApiBaseUrl('https://promview.example#x')).toThrow(/query or fragment/);
  });

  it('leaves the previous base in place when it refuses one', () => {
    setApiBaseUrl('https://promview.example');
    expect(() => setApiBaseUrl('nonsense')).toThrow();
    expect(apiUrl('/api/v1/alerts')).toBe('https://promview.example/api/v1/alerts');
  });
});

describe('the clients honour the configured base', () => {
  it('sends config, session and silence requests to the configured server', async () => {
    setApiBaseUrl('https://promview.example');

    const configFetch = vi.fn().mockResolvedValue(jsonResponse({ authMode: 'oidc' }));
    await loadRuntimeConfig(configFetch);
    expect(configFetch).toHaveBeenCalledWith('https://promview.example/api/v1/config');

    const sessionFetch = vi.fn().mockResolvedValue(
      jsonResponse({
        subject: 'ada',
        email: '',
        displayName: 'Ada',
        roles: [],
        anonymous: false,
      }),
    );
    await loadSession(sessionFetch);
    expect(sessionFetch).toHaveBeenCalledWith(`https://promview.example${SESSION_URL}`);

    const silenceFetch = vi
      .fn()
      .mockResolvedValue(jsonResponse({ endsAt: '', createdBy: 'ada', results: [] }, 201));
    await silenceAlert('42', { durationSeconds: 7200, comment: '' }, silenceFetch);
    expect(silenceFetch.mock.calls[0]?.[0]).toBe(
      'https://promview.example/api/v1/alerts/42/silence',
    );
  });
});
