import { afterEach, describe, expect, it, vi } from 'vitest';
import { connectHost, createHostFetch, hostPath } from './hostBridge';
import { apiBaseUrl, setApiBaseUrl } from './apiBase';
import { apiFetch, setApiFetch } from './transport';

afterEach(() => {
  setApiBaseUrl('');
  setApiFetch();
  delete (globalThis as Record<string, unknown>).__PROMVIEW_API_BASE__;
  delete (globalThis as Record<string, unknown>).__TAURI_INTERNALS__;
  vi.unstubAllGlobals();
});

describe('hostPath', () => {
  it('strips the configured base so the host is told what, not who', () => {
    expect(hostPath('https://promview.example/api/v1/alerts', 'https://promview.example')).toBe(
      '/api/v1/alerts',
    );
    expect(hostPath('https://ops.example/promview/api/v1/me', 'https://ops.example/promview')).toBe(
      '/api/v1/me',
    );
  });

  it('keeps a path that is already relative', () => {
    expect(hostPath('/api/v1/alerts?limit=50', '')).toBe('/api/v1/alerts?limit=50');
  });

  it('yields the root rather than an empty path', () => {
    expect(hostPath('https://promview.example', 'https://promview.example')).toBe('/');
  });

  it('refuses a URL that is not on the configured server', () => {
    // A page that could name the host could send the host's credentials
    // anywhere. Refusing beats silently rewriting it.
    expect(() => hostPath('https://evil.example/steal', 'https://promview.example')).toThrow(
      /refusing to route/,
    );
  });
});

describe('createHostFetch', () => {
  it('asks the host for a path and rebuilds a Response from the reply', async () => {
    const invoke = vi.fn().mockResolvedValue({
      status: 201,
      body: '{"ok":true}',
      headers: [['content-type', 'application/json']],
    });
    const hostFetch = createHostFetch(invoke, 'https://promview.example');

    const response = await hostFetch('https://promview.example/api/v1/alerts/1/silence', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: '{"comment":"x"}',
    });

    expect(invoke).toHaveBeenCalledWith('api_request', {
      request: {
        method: 'POST',
        path: '/api/v1/alerts/1/silence',
        body: '{"comment":"x"}',
        headers: [['Content-Type', 'application/json']],
      },
    });
    expect(response.status).toBe(201);
    expect(response.headers.get('content-type')).toBe('application/json');
    await expect(response.json()).resolves.toEqual({ ok: true });
  });

  it('defaults to GET and sends no body when there is none', async () => {
    const invoke = vi.fn().mockResolvedValue({ status: 200, body: '{}', headers: [] });
    await createHostFetch(invoke, 'https://promview.example')('/api/v1/config');

    const [, payload] = invoke.mock.calls[0] as [string, { request: Record<string, unknown> }];
    expect(payload.request.method).toBe('GET');
    expect(payload.request.body).toBeUndefined();
  });

  it('carries a Headers instance through', async () => {
    const invoke = vi.fn().mockResolvedValue({ status: 200, body: '{}', headers: [] });
    await createHostFetch(invoke, '')('/api/v1/me', {
      headers: new Headers({ accept: 'application/json' }),
    });

    const [, payload] = invoke.mock.calls[0] as [
      string,
      { request: { headers: [string, string][] } },
    ];
    expect(payload.request.headers).toEqual([['accept', 'application/json']]);
  });
});

describe('connectHost', () => {
  it('does nothing in a browser, where there is no host', () => {
    expect(connectHost()).toBe(false);
    expect(apiBaseUrl()).toBe('');
  });

  it('takes the base and the transport when the host offers both', async () => {
    const invoke = vi.fn().mockResolvedValue({ status: 200, body: '{}', headers: [] });
    (globalThis as Record<string, unknown>).__PROMVIEW_API_BASE__ = 'https://promview.example';
    (globalThis as Record<string, unknown>).__TAURI_INTERNALS__ = { invoke };

    expect(connectHost()).toBe(true);
    expect(apiBaseUrl()).toBe('https://promview.example');

    // Every client module defaults to apiFetch, so installing it here is what
    // routes the whole console through the host.
    await apiFetch('https://promview.example/api/v1/config');
    expect(invoke).toHaveBeenCalledTimes(1);
  });

  it('keeps the base but not the transport when the host offers no bridge', async () => {
    const fetchSpy = vi.fn().mockResolvedValue(new Response('{}'));
    vi.stubGlobal('fetch', fetchSpy);
    (globalThis as Record<string, unknown>).__PROMVIEW_API_BASE__ = 'https://promview.example';

    // Told where the server is but not how to reach it. Booting beats failing;
    // the requests are cross-origin and will say so plainly.
    expect(connectHost()).toBe(false);
    expect(apiBaseUrl()).toBe('https://promview.example');
    await apiFetch('/api/v1/config');
    expect(fetchSpy).toHaveBeenCalled();
  });
});
