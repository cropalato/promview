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
      // Open mode gates nothing, so the console never waits for a session.
      requiresSignIn: false,
      productName: 'Promview',
      // A backend that reports no silence fields predates silencing and cannot
      // serve it, so absent reads as off rather than as enabled.
      silenceEnabled: false,
      silenceDefaultSeconds: 2 * 60 * 60,
      silenceMaxSeconds: 30 * 24 * 60 * 60,
      // Likewise absent: a server that cannot resolve a group silence's real
      // match rejects the field outright rather than ignoring it, so the
      // console must not send one.
      silencePreviewSupported: false,
      silenceRemoveSupported: false,
    });
  });

  it('falls back to the default product name when it is missing', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse({ authMode: 'oidc' }));

    await expect(loadRuntimeConfig(fetchImpl)).resolves.toEqual({
      authMode: 'oidc',
      requiresSignIn: true,
      productName: 'Promview',
      silenceEnabled: false,
      silenceDefaultSeconds: 2 * 60 * 60,
      silenceMaxSeconds: 30 * 24 * 60 * 60,
      // Likewise absent: a server that cannot resolve a group silence's real
      // match rejects the field outright rather than ignoring it, so the
      // console must not send one.
      silencePreviewSupported: false,
      silenceRemoveSupported: false,
    });
  });

  it('takes the deployment silence window from the server', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(
      jsonResponse({
        authMode: 'oidc',
        silenceEnabled: true,
        silenceDefaultSeconds: 2700,
        silenceMaxSeconds: 28800,
      }),
    );

    await expect(loadRuntimeConfig(fetchImpl)).resolves.toMatchObject({
      silenceEnabled: true,
      silenceDefaultSeconds: 2700,
      silenceMaxSeconds: 28800,
    });
  });

  it('never offers a default window the server would refuse', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(
      jsonResponse({
        authMode: 'oidc',
        silenceEnabled: true,
        silenceDefaultSeconds: 999999,
        silenceMaxSeconds: 3600,
      }),
    );

    await expect(loadRuntimeConfig(fetchImpl)).resolves.toMatchObject({
      silenceDefaultSeconds: 3600,
      silenceMaxSeconds: 3600,
    });
  });

  it('accepts every mode the server can be running', async () => {
    for (const authMode of ['open', 'oidc', 'local'] as const) {
      const fetchImpl = vi.fn().mockResolvedValue(jsonResponse({ authMode }));
      await expect(loadRuntimeConfig(fetchImpl)).resolves.toMatchObject({ authMode });
    }
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

  it('rejects the removed ldap auth mode', () => {
    expect(() => parseRuntimeConfig({ authMode: 'ldap', productName: 'Promview' })).toThrowError(
      /unsupported auth mode/i,
    );
  });
});

describe('sign-in requirement', () => {
  it('takes the server at its word', () => {
    // The flag is the question the console asks, so a server that says a mode
    // needs no sign-in is believed over the console's own reading of the mode.
    expect(parseRuntimeConfig({ authMode: 'local', requiresSignIn: true }).requiresSignIn).toBe(
      true,
    );
    expect(parseRuntimeConfig({ authMode: 'oidc', requiresSignIn: false }).requiresSignIn).toBe(
      false,
    );
  });

  it('reads an older server that omits it from its mode', () => {
    // Absent must not read as false: the console would then fire
    // unauthenticated requests forever against a deployment that gates.
    expect(parseRuntimeConfig({ authMode: 'open' }).requiresSignIn).toBe(false);
    expect(parseRuntimeConfig({ authMode: 'oidc' }).requiresSignIn).toBe(true);
    expect(parseRuntimeConfig({ authMode: 'local' }).requiresSignIn).toBe(true);
  });
});

describe('silence preview capability', () => {
  it('reads an older server as unable to resolve a group silence', async () => {
    // The endpoint's absence is not something the console can probe for: an
    // older server rejects the whole request rather than ignoring the field.
    const fetchImpl = vi
      .fn()
      .mockResolvedValue(jsonResponse({ authMode: 'open', silenceEnabled: true }));
    await expect(loadRuntimeConfig(fetchImpl)).resolves.toMatchObject({
      silencePreviewSupported: false,
      silenceRemoveSupported: false,
    });
  });

  it('takes the server at its word when it says it can', async () => {
    const fetchImpl = vi
      .fn()
      .mockResolvedValue(
        jsonResponse({ authMode: 'open', silenceEnabled: true, silencePreviewSupported: true }),
      );
    await expect(loadRuntimeConfig(fetchImpl)).resolves.toMatchObject({
      silencePreviewSupported: true,
    });
  });
});

describe('silence removal capability', () => {
  it('reads an older server as unable to remove a silence', async () => {
    // A DELETE against a server without the route falls through to the SPA
    // route and answers a page, which reads as a broken console rather than a
    // missing feature. So the control is offered only where the server says so.
    const fetchImpl = vi
      .fn()
      .mockResolvedValue(jsonResponse({ authMode: 'open', silenceEnabled: true }));
    await expect(loadRuntimeConfig(fetchImpl)).resolves.toMatchObject({
      silenceRemoveSupported: false,
    });
  });

  it('takes the server at its word when it says it can', async () => {
    const fetchImpl = vi
      .fn()
      .mockResolvedValue(
        jsonResponse({ authMode: 'open', silenceEnabled: true, silenceRemoveSupported: true }),
      );
    await expect(loadRuntimeConfig(fetchImpl)).resolves.toMatchObject({
      silenceRemoveSupported: true,
    });
  });
});
