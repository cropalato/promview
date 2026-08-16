/**
 * Session client for protected deployments.
 *
 * `GET /api/v1/me` returns the effective principal when a session cookie (or,
 * later, a desktop bearer token) is present, and 401/403 otherwise. Sign-in is
 * a full-page navigation to `GET /api/v1/auth/oidc/login`; sign-out revokes
 * the opaque session through `POST /api/v1/auth/logout` and returns home.
 * The default transport is the browser's same-origin cookie flow; the injected
 * `SessionFetch`/`NavigateTo` seams are the compatibility points where the
 * future Tauri client supplies its bearer-capable transport and navigation.
 */
export const SESSION_URL = '/api/v1/me';
export const OIDC_LOGIN_URL = '/api/v1/auth/oidc/login';
export const LOGOUT_URL = '/api/v1/auth/logout';

/** The validated principal behind the current session. */
export interface SessionInfo {
  subject: string;
  email: string;
  /** Best available human label: displayName, then email, then subject. */
  displayName: string;
  roles: string[];
  anonymous: boolean;
}

export class SessionError extends Error {
  readonly status?: number;

  constructor(message: string, options: { status?: number; cause?: unknown } = {}) {
    super(message, options.cause !== undefined ? { cause: options.cause } : undefined);
    this.name = 'SessionError';
    this.status = options.status;
  }
}

export type SessionFetch = (url: string, init?: RequestInit) => Promise<Response>;

/**
 * Full-page navigation seam. Login and logout are server round-trips, not SPA
 * routes, so the app navigates through this injectable function instead of
 * touching `window.location` directly (readonly under jsdom, absent in Tauri
 * tests). Credentials never travel in URLs; the session cookie does the work.
 */
export type NavigateTo = (url: string) => void;

const defaultFetch: SessionFetch = (url, init) => fetch(url, init);

const browserNavigate: NavigateTo = (url) => {
  window.location.assign(url);
};

/**
 * Fetches and validates the current principal. A 401 maps to a SessionError
 * with `status: 401` (no session — the caller shows sign-in) and a 403 to
 * `status: 403` (session present but no read role).
 */
export async function loadSession(fetchImpl: SessionFetch = defaultFetch): Promise<SessionInfo> {
  let response: Response;
  try {
    response = await fetchImpl(SESSION_URL);
  } catch (cause) {
    throw new SessionError('Unable to reach the Promview API', { cause });
  }

  if (response.status === 401) {
    throw new SessionError('Authentication is required', { status: 401 });
  }
  if (response.status === 403) {
    throw new SessionError('This account does not have read access', { status: 403 });
  }
  if (!response.ok) {
    throw new SessionError(`Session request failed (HTTP ${response.status})`, {
      status: response.status,
    });
  }

  let body: unknown;
  try {
    body = await response.json();
  } catch (cause) {
    throw new SessionError('Session response was not valid JSON', { cause });
  }

  return parseSession(body);
}

export function parseSession(body: unknown): SessionInfo {
  if (typeof body !== 'object' || body === null || Array.isArray(body)) {
    throw new SessionError('Session response was malformed');
  }

  const record = body as Record<string, unknown>;
  if (typeof record.subject !== 'string' || record.subject === '') {
    throw new SessionError('Session response was malformed: subject must be a string');
  }
  const email = typeof record.email === 'string' ? record.email : '';
  const displayNameRaw = typeof record.displayName === 'string' ? record.displayName : '';
  return {
    subject: record.subject,
    email,
    displayName:
      displayNameRaw.trim() !== '' ? displayNameRaw : email !== '' ? email : record.subject,
    roles: stringList(record.roles),
    anonymous: record.anonymous === true,
  };
}

/** Known roles by ascending privilege; the highest one labels the identity. */
const ROLE_RANK: Record<string, number> = { viewer: 0, operator: 1, administrator: 2 };

export function highestRole(roles: readonly string[]): string | undefined {
  let best: string | undefined;
  let bestRank = -1;
  for (const role of roles) {
    const rank = ROLE_RANK[role];
    if (rank !== undefined && rank > bestRank) {
      best = role;
      bestRank = rank;
    }
  }
  return best;
}

/**
 * Revokes the server-side session and, only on success, navigates back to `/`
 * so the app reboots into the signed-out state. The server clears the session
 * cookie itself; failures surface to the caller without navigating.
 */
export async function endSession(
  fetchImpl: SessionFetch = defaultFetch,
  navigate: NavigateTo = browserNavigate,
): Promise<void> {
  let response: Response;
  try {
    response = await fetchImpl(LOGOUT_URL, { method: 'POST' });
  } catch (cause) {
    throw new SessionError('Unable to reach the Promview API', { cause });
  }
  if (!response.ok) {
    throw new SessionError(`Sign-out request failed (HTTP ${response.status})`, {
      status: response.status,
    });
  }
  navigate('/');
}

/** The server encodes an unset roles slice as null; treat both as empty. */
function stringList(value: unknown): string[] {
  if (value === null || value === undefined) {
    return [];
  }
  if (!Array.isArray(value)) {
    throw new SessionError('Session response was malformed: roles must be a list');
  }
  const result: string[] = [];
  for (const entry of value) {
    if (typeof entry !== 'string') {
      throw new SessionError('Session response was malformed: roles must be strings');
    }
    result.push(entry);
  }
  return result;
}
