import { describe, expect, it, vi } from 'vitest';
import {
  RUNTIME_CONFIG_URL,
  RuntimeConfigError,
  loadRuntimeConfig,
  parseRuntimeConfig,
} from './runtimeConfig';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

describe('loadRuntimeConfig', () => {
  it('requests exactly the runtime config endpoint', async () => {
    const fetchImpl = vi
      .fn()
      .mockResolvedValue(jsonResponse({ authMode: 'open', productName: 'Promview' }));

    await loadRuntimeConfig(fetchImpl);

    expect(fetchImpl).toHaveBeenCalledTimes(1);
    expect(fetchImpl).toHaveBeenCalledWith('/api/v1/config');
    expect(RUNTIME_CONFIG_URL).toBe('/api/v1/config');
  });

  it('parses an open-mode configuration', async () => {
    const fetchImpl = vi
      .fn()
      .mockResolvedValue(jsonResponse({ authMode: 'open', productName: 'Promview' }));

    await expect(loadRuntimeConfig(fetchImpl)).resolves.toEqual({
      authMode: 'open',
      productName: 'Promview',
    });
  });

  it('falls back to the default product name when it is missing', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse({ authMode: 'oidc' }));

    await expect(loadRuntimeConfig(fetchImpl)).resolves.toEqual({
      authMode: 'oidc',
      productName: 'Promview',
    });
  });

  it('rejects unsupported auth modes', async () => {
    const fetchImpl = vi
      .fn()
      .mockResolvedValue(jsonResponse({ authMode: 'kerberos', productName: 'Promview' }));

    await expect(loadRuntimeConfig(fetchImpl)).rejects.toThrowError(/unsupported auth mode/i);
  });

  it('fails with the HTTP status when the response is not ok', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse({ error: 'boom' }, 503));

    try {
      await loadRuntimeConfig(fetchImpl);
      expect.unreachable('loadRuntimeConfig should have thrown');
    } catch (error) {
      expect(error).toBeInstanceOf(RuntimeConfigError);
      expect((error as RuntimeConfigError).message).toMatch(/HTTP 503/);
      expect((error as RuntimeConfigError).status).toBe(503);
    }
  });

  it('wraps network failures', async () => {
    const fetchImpl = vi.fn().mockRejectedValue(new TypeError('fetch failed'));

    await expect(loadRuntimeConfig(fetchImpl)).rejects.toThrowError(/unable to reach/i);
  });

  it('rejects non-JSON responses', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(new Response('<html>nope</html>', { status: 200 }));

    await expect(loadRuntimeConfig(fetchImpl)).rejects.toThrowError(/not valid json/i);
  });
});

describe('parseRuntimeConfig', () => {
  it('rejects malformed payloads', () => {
    expect(() => parseRuntimeConfig(null)).toThrowError(/malformed/i);
    expect(() => parseRuntimeConfig('open')).toThrowError(/malformed/i);
  });
});
