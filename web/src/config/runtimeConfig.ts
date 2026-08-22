/**
 * Runtime configuration loaded from the Go backend.
 *
 * `GET /api/v1/config` is the only endpoint the shell calls today. It is
 * intentionally client-neutral (same-origin, cookie-based) so the same code
 * runs in the embedded browser build and the future Tauri desktop client.
 */
import { apiUrl } from './apiBase';
export type AuthMode = 'open' | 'oidc';

export interface RuntimeConfig {
  authMode: AuthMode;
  productName: string;
  /**
   * Whether this deployment can write silences to an Alertmanager at all, and
   * the window it allows. The console defaults and bounds its own duration
   * control from these rather than hardcoding one the server would reject.
   */
  silenceEnabled: boolean;
  silenceDefaultSeconds: number;
  silenceMaxSeconds: number;
}

export const RUNTIME_CONFIG_URL = '/api/v1/config';

const AUTH_MODES: readonly AuthMode[] = ['open', 'oidc'];
const DEFAULT_PRODUCT_NAME = 'Promview';
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
export async function loadRuntimeConfig(
  fetchImpl: FetchLike = (url) => fetch(url),
): Promise<RuntimeConfig> {
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

  const { authMode, productName, silenceEnabled, silenceDefaultSeconds, silenceMaxSeconds } =
    body as Record<string, unknown>;
  if (typeof authMode !== 'string' || !AUTH_MODES.includes(authMode as AuthMode)) {
    throw new RuntimeConfigError(`Unsupported auth mode: ${String(authMode)}`);
  }

  const max = positiveSeconds(silenceMaxSeconds, DEFAULT_SILENCE_MAX_SECONDS);
  return {
    authMode: authMode as AuthMode,
    productName:
      typeof productName === 'string' && productName.trim() !== ''
        ? productName
        : DEFAULT_PRODUCT_NAME,
    // A backend that does not report the field predates silencing and cannot
    // serve it, so absent reads as off rather than as enabled.
    silenceEnabled: silenceEnabled === true,
    silenceMaxSeconds: max,
    // Never offer a default the server would refuse.
    silenceDefaultSeconds: Math.min(
      positiveSeconds(silenceDefaultSeconds, DEFAULT_SILENCE_SECONDS),
      max,
    ),
  };
}
