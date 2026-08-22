/**
 * Who actually makes the console's API requests.
 *
 * In the browser that is the browser: same-origin, cookies handled for us, and
 * nothing to decide. A host shell is different. Its webview is a local page
 * talking to a remote server, so its own requests are cross-origin, and the
 * credentials it would need are exactly what should not be reachable from page
 * script. Such a shell installs its own caller here and every client module
 * follows, because they all default to `apiFetch` rather than to `fetch`.
 *
 * This is module state for the same reason `apiBase` is: the transport is a
 * property of the process, chosen once at startup, not of any single request.
 */

export type ApiFetch = (url: string, init?: RequestInit) => Promise<Response>;

// `same-origin` is already fetch's default, so setting it explicitly would add
// nothing but noise to every call site and every assertion about one. The init
// argument is forwarded only when there is one, so a caller that passed nothing
// still makes a one-argument call.
const browserFetch: ApiFetch = (url, init) => (init === undefined ? fetch(url) : fetch(url, init));

let current: ApiFetch = browserFetch;

/** Installs a caller. Passing nothing restores the browser's own fetch. */
export function setApiFetch(next?: ApiFetch): void {
  current = next ?? browserFetch;
}

/**
 * The caller every client module uses by default. Read through this indirection
 * rather than captured, so a shell that installs late still takes effect.
 */
export const apiFetch: ApiFetch = (url, init) =>
  // Forward only what the caller passed: synthesising an `undefined` second
  // argument changes the call a host caller observes, for no benefit.
  init === undefined ? current(url) : current(url, init);
