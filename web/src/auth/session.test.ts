import { describe, expect, it, vi } from 'vitest';
import {
  LOGIN_URL,
  LOGOUT_URL,
  OIDC_LOGIN_URL,
  SESSION_URL,
  SessionError,
  endSession,
  highestRole,
  loadSession,
  parseSession,
  signIn,
  canAdminister,
  canOperate,
} from './session';
import type { SessionInfo } from './session';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const PRINCIPAL = {
  subject: 'https://idp.example|user-1',
  email: 'ada@example.com',
  displayName: 'Ada Lovelace',
  roles: ['operator'],
  anonymous: false,
};

describe('loadSession', () => {
  it('requests exactly the session endpoint', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse(PRINCIPAL));

    await loadSession(fetchImpl);

    expect(fetchImpl).toHaveBeenCalledTimes(1);
    expect(fetchImpl).toHaveBeenCalledWith('/api/v1/me');
    expect(SESSION_URL).toBe('/api/v1/me');
  });

  it('parses an authenticated principal', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse(PRINCIPAL));

    await expect(loadSession(fetchImpl)).resolves.toEqual({
      subject: 'https://idp.example|user-1',
      email: 'ada@example.com',
      displayName: 'Ada Lovelace',
      roles: ['operator'],
      anonymous: false,
    });
  });

  it('flags 401 responses as unauthenticated', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse({ error: 'nope' }, 401));

    try {
      await loadSession(fetchImpl);
      expect.unreachable('loadSession should have thrown');
    } catch (error) {
      expect(error).toBeInstanceOf(SessionError);
      expect((error as SessionError).status).toBe(401);
    }
  });

  it('flags 403 responses as access denied', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse({ error: 'nope' }, 403));

    try {
      await loadSession(fetchImpl);
      expect.unreachable('loadSession should have thrown');
    } catch (error) {
      expect(error).toBeInstanceOf(SessionError);
      expect((error as SessionError).status).toBe(403);
    }
  });

  it('fails with the HTTP status for other server errors', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse({ error: 'boom' }, 500));

    try {
      await loadSession(fetchImpl);
      expect.unreachable('loadSession should have thrown');
    } catch (error) {
      expect(error).toBeInstanceOf(SessionError);
      expect((error as SessionError).message).toMatch(/HTTP 500/);
      expect((error as SessionError).status).toBe(500);
    }
  });

  it('wraps network failures', async () => {
    const fetchImpl = vi.fn().mockRejectedValue(new TypeError('fetch failed'));

    await expect(loadSession(fetchImpl)).rejects.toThrowError(/unable to reach/i);
  });

  it('rejects non-JSON responses', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(new Response('<html>nope</html>', { status: 200 }));

    await expect(loadSession(fetchImpl)).rejects.toThrowError(/not valid json/i);
  });
});

describe('parseSession', () => {
  it('rejects malformed payloads', () => {
    expect(() => parseSession(null)).toThrowError(/malformed/i);
    expect(() => parseSession('ada')).toThrowError(/malformed/i);
    expect(() => parseSession({ roles: [] })).toThrowError(/malformed/i);
    expect(() => parseSession({ subject: 's', roles: 'viewer' })).toThrowError(/malformed/i);
    expect(() => parseSession({ subject: 's', roles: ['viewer', 7] })).toThrowError(/malformed/i);
  });

  it('falls back to the email, then the subject, for the display name', () => {
    expect(parseSession({ subject: 's-1', email: 'a@example.com', roles: [] }).displayName).toBe(
      'a@example.com',
    );
    expect(parseSession({ subject: 's-1', roles: [] }).displayName).toBe('s-1');
  });

  it('tolerates a null roles list and defaults anonymous to false', () => {
    expect(parseSession({ subject: 's-1', roles: null })).toEqual({
      subject: 's-1',
      email: '',
      displayName: 's-1',
      roles: [],
      anonymous: false,
    });
  });
});

describe('highestRole', () => {
  it('returns the most privileged known role', () => {
    expect(highestRole(['viewer', 'administrator'])).toBe('administrator');
    expect(highestRole(['operator', 'viewer'])).toBe('operator');
    expect(highestRole(['viewer'])).toBe('viewer');
  });

  it('returns undefined when no known role is present', () => {
    expect(highestRole([])).toBeUndefined();
    expect(highestRole(['superuser'])).toBeUndefined();
  });
});

describe('signIn', () => {
  const credentials = { username: 'ada', password: 'correct horse battery staple' };

  it('posts the credentials in the body of the login endpoint', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));

    await expect(signIn(credentials, fetchImpl)).resolves.toBeUndefined();

    expect(LOGIN_URL).toBe('/api/v1/auth/login');
    const [url, init] = fetchImpl.mock.calls[0] as [string, RequestInit];
    expect(url).toBe('/api/v1/auth/login');
    expect(init.method).toBe('POST');
    expect(JSON.parse(String(init.body))).toEqual(credentials);
  });

  it('never puts the password anywhere but the body', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));

    await signIn(credentials, fetchImpl);

    // A password in a URL survives in access logs, Referer headers and
    // browser history; in web storage it survives the tab.
    const [url, init] = fetchImpl.mock.calls[0] as [string, RequestInit];
    expect(url).not.toContain(credentials.password);
    expect(JSON.stringify(init.headers)).not.toContain(credentials.password);
    expect(window.localStorage.length).toBe(0);
    expect(window.sessionStorage.length).toBe(0);
  });

  it('reports a 401 without saying which part was wrong', async () => {
    const fetchImpl = vi
      .fn()
      .mockResolvedValue(new Response('invalid credentials', { status: 401 }));

    try {
      await signIn(credentials, fetchImpl);
      expect.unreachable('signIn should have thrown');
    } catch (error) {
      expect(error).toBeInstanceOf(SessionError);
      expect((error as SessionError).status).toBe(401);
      // The server answers the same way for an unknown username, a wrong
      // password, a disabled account and a locked one, so nothing here may
      // name which of them it was.
      expect((error as SessionError).message).not.toMatch(/no such|unknown|disabled|locked/i);
    }
  });

  it('maps a 403 to the no-read-access failure', async () => {
    const fetchImpl = vi
      .fn()
      .mockResolvedValue(new Response('read access denied', { status: 403 }));

    try {
      await signIn(credentials, fetchImpl);
      expect.unreachable('signIn should have thrown');
    } catch (error) {
      expect((error as SessionError).status).toBe(403);
      expect((error as SessionError).message).toMatch(/read access/i);
    }
  });

  it('carries the wait the server asked for on a 429', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(
      new Response('too many sign-in attempts', {
        status: 429,
        headers: { 'Retry-After': '45' },
      }),
    );

    try {
      await signIn(credentials, fetchImpl);
      expect.unreachable('signIn should have thrown');
    } catch (error) {
      expect((error as SessionError).status).toBe(429);
      expect((error as SessionError).retryAfterSeconds).toBe(45);
    }
  });

  it('leaves the wait unset when the server did not say', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(new Response(null, { status: 429 }));

    try {
      await signIn(credentials, fetchImpl);
      expect.unreachable('signIn should have thrown');
    } catch (error) {
      expect((error as SessionError).retryAfterSeconds).toBeUndefined();
    }
  });

  it('fails with the HTTP status for anything else', async () => {
    // A broken directory is not a wrong password, and must not read as one.
    const fetchImpl = vi.fn().mockResolvedValue(new Response('upstream', { status: 502 }));

    try {
      await signIn(credentials, fetchImpl);
      expect.unreachable('signIn should have thrown');
    } catch (error) {
      expect((error as SessionError).status).toBe(502);
      expect((error as SessionError).message).toMatch(/HTTP 502/);
    }
  });

  it('wraps network failures', async () => {
    const fetchImpl = vi.fn().mockRejectedValue(new TypeError('fetch failed'));

    await expect(signIn(credentials, fetchImpl)).rejects.toThrowError(/unable to reach/i);
  });
});

describe('endSession', () => {
  it('posts to the logout endpoint and navigates home on success', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    const navigate = vi.fn();

    await endSession(fetchImpl, navigate);

    expect(fetchImpl).toHaveBeenCalledTimes(1);
    expect(fetchImpl).toHaveBeenCalledWith('/api/v1/auth/logout', { method: 'POST' });
    expect(navigate).toHaveBeenCalledWith('/');
    expect(LOGOUT_URL).toBe('/api/v1/auth/logout');
    expect(OIDC_LOGIN_URL).toBe('/api/v1/auth/oidc/login');
  });

  it('does not navigate when the server rejects the request', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse({ error: 'boom' }, 500));
    const navigate = vi.fn();

    await expect(endSession(fetchImpl, navigate)).rejects.toThrowError(/HTTP 500/);
    expect(navigate).not.toHaveBeenCalled();
  });

  it('does not navigate on network failures', async () => {
    const fetchImpl = vi.fn().mockRejectedValue(new TypeError('fetch failed'));
    const navigate = vi.fn();

    await expect(endSession(fetchImpl, navigate)).rejects.toThrowError(/unable to reach/i);
    expect(navigate).not.toHaveBeenCalled();
  });
});

describe('canOperate', () => {
  function session(overrides: Partial<SessionInfo> = {}): SessionInfo {
    return {
      subject: 'ada',
      email: 'ada@example.com',
      displayName: 'Ada',
      roles: ['operator'],
      anonymous: false,
      ...overrides,
    };
  }

  it('allows operators and administrators', () => {
    expect(canOperate(session({ roles: ['operator'] }))).toBe(true);
    expect(canOperate(session({ roles: ['administrator'] }))).toBe(true);
    expect(canOperate(session({ roles: ['viewer', 'operator'] }))).toBe(true);
  });

  it('refuses a viewer, an unknown role, and no session at all', () => {
    expect(canOperate(session({ roles: ['viewer'] }))).toBe(false);
    expect(canOperate(session({ roles: ['unmapped'] }))).toBe(false);
    expect(canOperate(session({ roles: [] }))).toBe(false);
    expect(canOperate(undefined)).toBe(false);
  });

  it('allows an elevated open-mode principal, anonymous though it is', () => {
    // This mirrors internal/auth/authorization.go: the server dropped its
    // short-circuit on anonymous and now honours the roles an open deployment
    // was told to grant. Refusing here would hide every control such a
    // deployment exists to offer.
    expect(canOperate(session({ anonymous: true, roles: ['viewer', 'operator'] }))).toBe(true);
    expect(canOperate(session({ anonymous: true, roles: ['viewer', 'administrator'] }))).toBe(true);
  });

  it('still refuses the plain anonymous viewer an unelevated open mode grants', () => {
    // The case an over-broad change would break: dropping the anonymity test
    // must not turn every open deployment into an operating one.
    expect(canOperate(session({ anonymous: true, roles: ['viewer'] }))).toBe(false);
    expect(canOperate(session({ anonymous: true, roles: [] }))).toBe(false);
  });
});

describe('canAdminister', () => {
  function session(overrides: Partial<SessionInfo> = {}): SessionInfo {
    return {
      subject: 'ada',
      email: 'ada@example.com',
      displayName: 'Ada',
      roles: ['administrator'],
      anonymous: false,
      ...overrides,
    };
  }

  it('allows administrators only', () => {
    expect(canAdminister(session({ roles: ['administrator'] }))).toBe(true);
    expect(canAdminister(session({ roles: ['operator'] }))).toBe(false);
    expect(canAdminister(session({ roles: ['viewer'] }))).toBe(false);
    expect(canAdminister(undefined)).toBe(false);
  });

  it('allows an anonymous administrator where the deployment asked for one', () => {
    // The server permits it, so a console that refused would be lying about
    // what the deployment does.
    expect(canAdminister(session({ anonymous: true, roles: ['viewer', 'administrator'] }))).toBe(
      true,
    );
  });

  it('still refuses the plain anonymous viewer and the anonymous operator', () => {
    expect(canAdminister(session({ anonymous: true, roles: ['viewer'] }))).toBe(false);
    expect(canAdminister(session({ anonymous: true, roles: ['viewer', 'operator'] }))).toBe(false);
  });
});
