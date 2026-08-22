/**
 * Where the Promview API lives.
 *
 * The browser console talks to its own origin, so every path it builds is
 * relative and the base is empty. The desktop shell has no origin to be
 * relative to: it is a local webview pointed at a server the operator
 * configured, and `docs/desktop-client-plan.md` asks the API client to accept
 * an injected base URL for exactly that reason.
 *
 * This is module state rather than a parameter threaded through every call
 * because the base is a property of the process, fixed once at startup, not of
 * any individual request. A shell sets it before React mounts; nothing else
 * ever changes it, and the browser never calls the setter at all.
 */

let base = '';

/**
 * Points the client at a server. An empty value restores same-origin relative
 * paths, which is what the browser build uses.
 *
 * Throws on a URL that would silently produce unreachable requests: a relative
 * base, or one carrying a query or fragment that path joining would strip.
 */
export function setApiBaseUrl(url: string): void {
  if (url === '') {
    base = '';
    return;
  }
  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    throw new Error(`API base URL must be absolute, got ${url}`);
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    throw new Error(`API base URL must be http or https, got ${parsed.protocol}`);
  }
  if (parsed.search !== '' || parsed.hash !== '') {
    // Joining a path onto these would drop them, so the request would not go
    // where the caller asked. Better to refuse than to quietly misroute.
    throw new Error('API base URL must not carry a query or fragment');
  }
  // Stored without a trailing slash so joining is a plain concatenation.
  base = `${parsed.origin}${parsed.pathname.replace(/\/+$/, '')}`;
}

export function apiBaseUrl(): string {
  return base;
}

/** Resolves an absolute API path against the configured base. */
export function apiUrl(path: string): string {
  return base === '' ? path : `${base}${path}`;
}
