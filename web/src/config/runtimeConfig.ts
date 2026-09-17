/**
 * Runtime configuration loaded from the Go backend.
 *
 * `GET /api/v1/config` is the only endpoint the shell calls today. It is
 * intentionally client-neutral (same-origin, cookie-based) so the same code
 * runs in the embedded browser build and the future Tauri desktop client.
 */
import { apiUrl } from './apiBase';
import { apiFetch } from './transport';
export type AuthMode = 'open' | 'oidc' | 'local' | 'ldap';
export type OpenModeRole = 'viewer' | 'operator' | 'administrator';

export interface RuntimeConfig {
  authMode: AuthMode;
  /**
   * What the anonymous principal open mode hands every reader is allowed to
   * do, and the name its actions are recorded under. A deployment can elevate
   * that principal past reading, and then everyone who can reach the port can
   * act under one shared name — which is the one thing the console has to be
   * able to say out loud, because nobody is looking for it.
   */
  openModeRole: OpenModeRole;
  openModeAuthor: string;
  /**
   * Whether this deployment gates the console behind a sign-in. The console
   * asks this rather than testing for a particular mode, so a mode it has
   * never heard of still gates correctly instead of falling open.
   */
  requiresSignIn: boolean;
  productName: string;
  /**
   * Whether this deployment can write silences to an Alertmanager at all, and
   * the window it allows. The console defaults and bounds its own duration
   * control from these rather than hardcoding one the server would reject.
   */
  silenceEnabled: boolean;
  silenceDefaultSeconds: number;
  silenceMaxSeconds: number;
  /**
   * Whether the server can resolve what a group silence would actually match.
   * A console ships and updates independently of the server behind it, so it
   * cannot assume the endpoint exists just because it knows the name: an older
   * server rejects the whole request rather than ignoring the field it does not
   * know. Absent reads as unsupported, and the console silences on the grouping
   * key as it always did.
   */
  silencePreviewSupported: boolean;
  /**
   * Whether the server can remove a silence. Same reason as the preview flag:
   * a console offering a Remove control against an older server would send a
   * request that falls through to the SPA route and answers a page, which
   * reads as a broken console rather than a missing feature. Absent reads as
   * unsupported and the control is not offered.
   */
  silenceRemoveSupported: boolean;
}

export const RUNTIME_CONFIG_URL = '/api/v1/config';

// An unrecognised mode is refused rather than tolerated. The console is served
// by the same binary that reports the mode, so version skew is bounded and a
// value from outside this list means the deployment is broken, not that the
// console is old — and guessing would gate, or fail to gate, on an auth model
// nobody here has seen.
const AUTH_MODES: readonly AuthMode[] = ['open', 'oidc', 'local', 'ldap'];
// Unlike authMode, a role the console does not recognise is not fatal: nothing
// here has to understand it to render alerts, and the worst a fallback costs is
// a hidden control the server would have accepted. Reading it as elevated would
// cost a banner announcing access nobody has.
const OPEN_MODE_ROLES: readonly OpenModeRole[] = ['viewer', 'operator', 'administrator'];
const DEFAULT_PRODUCT_NAME = 'Promview';
// Mirrors the server's own fallback, so an older backend that reports no author
// is described by the name it would actually record actions under.
const DEFAULT_OPEN_MODE_AUTHOR = 'promview-open-mode';
// Mirrors the server's own defaults, used only when an older backend does not
// report them. Two hours, capped at thirty days.
const DEFAULT_SILENCE_SECONDS = 2 * 60 * 60;
const DEFAULT_SILENCE_MAX_SECONDS = 30 * 24 * 60 * 60;

function positiveSeconds(value: unknown, fallback: number): number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0
    ? Math.floor(value)
    : fallback;
}

export class RuntimeConfigError extends Error {
  readonly status?: number;

  constructor(message: string, options: { status?: number; cause?: unknown } = {}) {
    super(message, options.cause !== undefined ? { cause: options.cause } : undefined);
    this.name = 'RuntimeConfigError';
    this.status = options.status;
  }
}

type FetchLike = (url: string) => Promise<Response>;

/**
 * Fetches and validates the runtime configuration. Same-origin credentials
 * (the fetch default) keep browser session cookies flowing without any
 * client-side transport branching.
 */
export async function loadRuntimeConfig(fetchImpl: FetchLike = apiFetch): Promise<RuntimeConfig> {
  let response: Response;
  try {
    response = await fetchImpl(apiUrl(RUNTIME_CONFIG_URL));
  } catch (cause) {
    throw new RuntimeConfigError('Unable to reach the Promview API', { cause });
  }

  if (!response.ok) {
    throw new RuntimeConfigError(`Configuration request failed (HTTP ${response.status})`, {
      status: response.status,
    });
  }

  let body: unknown;
  try {
    body = await response.json();
  } catch (cause) {
    throw new RuntimeConfigError('Configuration response was not valid JSON', { cause });
  }

  return parseRuntimeConfig(body);
}

export function parseRuntimeConfig(body: unknown): RuntimeConfig {
  if (typeof body !== 'object' || body === null) {
    throw new RuntimeConfigError('Configuration response was malformed');
  }

  const {
    authMode,
    requiresSignIn,
    openModeRole,
    openModeAuthor,
    productName,
    silenceEnabled,
    silenceDefaultSeconds,
    silenceMaxSeconds,
    silencePreviewSupported,
    silenceRemoveSupported,
  } = body as Record<string, unknown>;
  if (typeof authMode !== 'string' || !AUTH_MODES.includes(authMode as AuthMode)) {
    throw new RuntimeConfigError(`Unsupported auth mode: ${String(authMode)}`);
  }

  const mode = authMode as AuthMode;
  const max = positiveSeconds(silenceMaxSeconds, DEFAULT_SILENCE_MAX_SECONDS);
  return {
    authMode: mode,
    // A server too old to report the flag still has a mode, and every mode but
    // open issues sessions. Reading absent as false would leave the console
    // firing unauthenticated requests forever against a deployment that gates.
    requiresSignIn: typeof requiresSignIn === 'boolean' ? requiresSignIn : mode !== 'open',
    // A server too old to report elevation cannot be doing any: absent reads as
    // the unelevated deployment open mode has always been.
    openModeRole:
      typeof openModeRole === 'string' && OPEN_MODE_ROLES.includes(openModeRole as OpenModeRole)
        ? (openModeRole as OpenModeRole)
        : 'viewer',
    openModeAuthor:
      typeof openModeAuthor === 'string' && openModeAuthor.trim() !== ''
        ? openModeAuthor
        : DEFAULT_OPEN_MODE_AUTHOR,
    productName:
      typeof productName === 'string' && productName.trim() !== ''
        ? productName
        : DEFAULT_PRODUCT_NAME,
    // A backend that does not report the field predates silencing and cannot
    // serve it, so absent reads as off rather than as enabled.
    silenceEnabled: silenceEnabled === true,
    silencePreviewSupported: silencePreviewSupported === true,
    silenceRemoveSupported: silenceRemoveSupported === true,
    silenceMaxSeconds: max,
    // Never offer a default the server would refuse.
    silenceDefaultSeconds: Math.min(
      positiveSeconds(silenceDefaultSeconds, DEFAULT_SILENCE_SECONDS),
      max,
    ),
  };
}
