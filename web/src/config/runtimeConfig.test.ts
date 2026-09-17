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
      // A server that reports no elevation cannot be doing any.
      openModeRole: 'viewer',
      openModeAuthor: 'promview-open-mode',
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
      openModeRole: 'viewer',
      openModeAuthor: 'promview-open-mode',
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
    for (const authMode of ['open', 'oidc', 'local', 'ldap'] as const) {
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

  it('still refuses a mode nobody here has seen', () => {
    // Guessing would gate, or fail to gate, on an auth model this console has
    // no code for, so an unknown mode stays a hard failure even now that the
    // list has grown.
    expect(() => parseRuntimeConfig({ authMode: 'saml', productName: 'Promview' })).toThrowError(
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
    expect(parseRuntimeConfig({ authMode: 'ldap' }).requiresSignIn).toBe(true);
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

describe('open-mode elevation', () => {
  it('takes the role and the author the deployment reports', () => {
    const config = parseRuntimeConfig({
      authMode: 'open',
      openModeRole: 'operator',
      openModeAuthor: 'lab-console',
    });

    expect(config.openModeRole).toBe('operator');
    expect(config.openModeAuthor).toBe('lab-console');
  });

  it('accepts every role an open deployment can be granting', () => {
    for (const openModeRole of ['viewer', 'operator', 'administrator'] as const) {
      expect(parseRuntimeConfig({ authMode: 'open', openModeRole }).openModeRole).toBe(
        openModeRole,
      );
    }
  });

  it('reads a server that reports neither field as unelevated', () => {
    // A backend too old to report elevation cannot be doing any, and guessing
    // the other way would put a banner on screen claiming access nobody has.
    const config = parseRuntimeConfig({ authMode: 'open' });

    expect(config.openModeRole).toBe('viewer');
    expect(config.openModeAuthor).toBe('promview-open-mode');
  });

  it('falls back to viewer for a role it does not know, rather than throwing', () => {
    // Unlike authMode, this is not a value the console has to understand to
    // render alerts: the cost of the fallback is a hidden control the server
    // would have accepted, which beats refusing to boot.
    expect(parseRuntimeConfig({ authMode: 'open', openModeRole: 'superuser' }).openModeRole).toBe(
      'viewer',
    );
    expect(parseRuntimeConfig({ authMode: 'open', openModeRole: 7 }).openModeRole).toBe('viewer');
  });

  it('falls back to the server default for an empty author', () => {
    expect(parseRuntimeConfig({ authMode: 'open', openModeAuthor: '   ' }).openModeAuthor).toBe(
      'promview-open-mode',
    );
  });
});
