/**
 * Session client for protected deployments.
 *
 * `GET /api/v1/me` returns the effective principal when a session cookie (or,
 * later, a desktop bearer token) is present, and 401/403 otherwise. Sign-in is
 * a full-page navigation to `GET /api/v1/auth/oidc/login` under OIDC, or a
 * `POST /api/v1/auth/login` with a username and password where the deployment
 * keeps its own accounts; sign-out revokes the opaque session through
 * `POST /api/v1/auth/logout` and returns home.
 * The default transport is the browser's same-origin cookie flow; the injected
 * `SessionFetch`/`NavigateTo` seams are the compatibility points where the
 * future Tauri client supplies its bearer-capable transport and navigation.
 */
import { apiUrl } from '../config/apiBase';
import { apiFetch } from '../config/transport';
export const SESSION_URL = '/api/v1/me';
export const OIDC_LOGIN_URL = '/api/v1/auth/oidc/login';
export const LOGIN_URL = '/api/v1/auth/login';
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
  /**
   * How long the server asked the caller to wait, in seconds. Only a throttled
   * sign-in carries one, and only when the server said so: a form that invents
   * a number is telling the operator something nobody promised.
   */
  readonly retryAfterSeconds?: number;

  constructor(
    message: string,
    options: { status?: number; retryAfterSeconds?: number; cause?: unknown } = {},
  ) {
    super(message, options.cause !== undefined ? { cause: options.cause } : undefined);
    this.name = 'SessionError';
    this.status = options.status;
    this.retryAfterSeconds = options.retryAfterSeconds;
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

const defaultFetch: SessionFetch = apiFetch;

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
    response = await fetchImpl(apiUrl(SESSION_URL));
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

/**
 * Signs in with a username and password against a deployment that keeps local
 * accounts.
 *
 * The credentials go in the request body and nowhere else. A password in a URL
 * survives in access logs, in `Referer` headers and in browser history, and a
 * password in web storage survives the tab; this one lives only as long as the
 * request. Success is 204 with the session in an HttpOnly cookie, so there is
 * no body to read and nothing here to keep — the caller re-checks
 * `/api/v1/me` to learn who it is now.
 */
export async function signIn(
  credentials: { username: string; password: string },
  fetchImpl: SessionFetch = defaultFetch,
): Promise<void> {
  let response: Response;
  try {
    response = await fetchImpl(apiUrl(LOGIN_URL), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: credentials.username, password: credentials.password }),
    });
  } catch (cause) {
    throw new SessionError('Unable to reach the Promview API', { cause });
  }

  if (response.status === 401) {
    // The server answers identically for an unknown username, a wrong
    // password, a disabled account and a locked one. Telling them apart here
    // would rebuild the oracle the server refuses to be.
    throw new SessionError('Incorrect username or password', { status: 401 });
  }
  if (response.status === 403) {
    throw new SessionError('This account does not have read access', { status: 403 });
  }
  if (response.status === 429) {
    throw new SessionError('Too many sign-in attempts', {
      status: 429,
      retryAfterSeconds: parseRetryAfter(response.headers.get('Retry-After')),
    });
  }
  if (!response.ok) {
    throw new SessionError(`Sign-in request failed (HTTP ${response.status})`, {
      status: response.status,
    });
  }
}

/**
 * The server sends whole seconds. The header also permits an HTTP date, which
 * this one never sends, so anything unparseable is dropped rather than guessed
 * at: no advice beats wrong advice about how long to wait.
 */
function parseRetryAfter(header: string | null): number | undefined {
  if (header === null) {
    return undefined;
  }
  const seconds = Number(header.trim());
  return Number.isFinite(seconds) && seconds > 0 ? Math.ceil(seconds) : undefined;
}

/** Known roles by ascending privilege; the highest one labels the identity. */
const ROLE_RANK: Record<string, number> = { viewer: 0, operator: 1, administrator: 2 };

/**
 * canOperate and canAdminister are a mirror of `internal/auth/authorization.go`
 * (CanOperate and CanAdminister) and must not drift from it. The server is the
 * authority and re-checks both on every mutation; these two only decide whether
 * the console offers the control at all, and a console that disagrees with the
 * server is wrong in one of two ways — offering a control whose every request
 * answers 403, or hiding one the deployment deliberately granted.
 *
 * Both decide on the roles alone, with no test for anonymity. An open-mode
 * deployment can be told to hand its anonymous principal an operator or
 * administrator role, and the server honours it; refusing here would hide every
 * control such a deployment exists to offer.
 */
export function canOperate(session: SessionInfo | undefined): boolean {
  if (session === undefined) {
    return false;
  }
  return session.roles.some((role) => role === 'operator' || role === 'administrator');
}

/**
 * Administrator only: the server refuses an operator too, because changing who
 * can do what is not an operator action.
 */
export function canAdminister(session: SessionInfo | undefined): boolean {
  if (session === undefined) {
    return false;
  }
  return session.roles.some((role) => role === 'administrator');
}

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
    response = await fetchImpl(apiUrl(LOGOUT_URL), { method: 'POST' });
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
