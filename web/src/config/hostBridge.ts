import { setApiBaseUrl } from './apiBase';
import { setEventSourceFactory } from '../alerts/stream';
import { createHostEventSourceFactory } from './hostStream';
import { setApiFetch } from './transport';

/**
 * Wiring the console into a host shell.
 *
 * The shell injects two things before any application script runs: the server
 * it was configured for, and an invoke bridge. When both are present the
 * console stops making requests itself and asks the host to make them, which is
 * what lets a local webview talk to a remote server at all — its own requests
 * would be cross-origin — and what keeps session credentials in the host where
 * page script cannot read them.
 *
 * In a browser neither is present and nothing here changes: the console keeps
 * its relative paths and its own fetch.
 */

interface HostResponse {
  status: number;
  body: string;
  headers: [string, string][];
}

type Invoke = (command: string, payload: unknown) => Promise<unknown>;

interface HostGlobals {
  __PROMVIEW_API_BASE__?: unknown;
  __TAURI_INTERNALS__?: { invoke?: unknown };
}

function hostInvoke(): Invoke | undefined {
  const internals = (globalThis as HostGlobals).__TAURI_INTERNALS__;
  return typeof internals?.invoke === 'function' ? (internals.invoke as Invoke) : undefined;
}

/**
 * Turns an absolute URL back into the path the host expects. The host resolves
 * it against the server it was configured with, so the page names what it wants
 * and never who to ask — a page that could name the host could send the host's
 * credentials anywhere.
 */
export function hostPath(url: string, base: string): string {
  if (base !== '' && url.startsWith(base)) {
    return url.slice(base.length) || '/';
  }
  if (url.startsWith('/')) {
    return url;
  }
  // Not on the configured server. Refusing beats silently rewriting it.
  throw new Error(`refusing to route ${url} through the host`);
}

function headerPairs(init?: RequestInit): [string, string][] {
  const headers = init?.headers;
  if (headers === undefined) {
    return [];
  }
  if (headers instanceof Headers) {
    return [...headers.entries()];
  }
  if (Array.isArray(headers)) {
    return headers.map(([name, value]) => [name, value]);
  }
  return Object.entries(headers);
}

/** Builds the fetch that routes through the host's invoke bridge. */
export function createHostFetch(invoke: Invoke, base: string) {
  return async (url: string, init?: RequestInit): Promise<Response> => {
    const raw = await invoke('api_request', {
      request: {
        method: (init?.method ?? 'GET').toUpperCase(),
        path: hostPath(url, base),
        body: typeof init?.body === 'string' ? init.body : undefined,
        headers: headerPairs(init),
      },
    });
    const response = raw as HostResponse;
    return new Response(response.body, {
      status: response.status,
      headers: new Headers(response.headers),
    });
  };
}

/**
 * Installs the host wiring when there is a host. Returns whether it did, which
 * is only of interest to tests and to the boot log.
 */
export function connectHost(): boolean {
  const base = (globalThis as HostGlobals).__PROMVIEW_API_BASE__;
  if (typeof base !== 'string' || base === '') {
    return false;
  }
  setApiBaseUrl(base);

  const invoke = hostInvoke();
  if (invoke === undefined) {
    // A base with no bridge: the shell told us where the server is but not how
    // to reach it. Leave the browser's fetch in place rather than failing to
    // boot; the requests are cross-origin and will say so plainly.
    return false;
  }
  setApiFetch(createHostFetch(invoke, base));
  // The stream goes the same way, and for a stronger reason than CORS: one the
  // webview owns dies with the window, and the tray has to keep counting after
  // the last one closes.
  setEventSourceFactory(createHostEventSourceFactory(invoke, base));
  return true;
}
