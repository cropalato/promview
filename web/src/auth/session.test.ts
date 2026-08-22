import { describe, expect, it, vi } from 'vitest';
import {
  LOGOUT_URL,
  OIDC_LOGIN_URL,
  SESSION_URL,
  SessionError,
  endSession,
  highestRole,
  loadSession,
  parseSession,
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

  it('refuses the anonymous reader open mode grants', () => {
    // Open mode hands every reader the same anonymous viewer, so an operator
    // control there could only ever answer 403.
    expect(canOperate(session({ anonymous: true, roles: ['administrator'] }))).toBe(false);
  });
});
