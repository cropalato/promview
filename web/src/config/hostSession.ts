/**
 * Sign-in owned by the host shell.
 *
 * In a browser, sign-in is a full-page navigation: the OIDC round-trip sets a
 * cookie and comes back. A host webview must not make that trip — navigating
 * would leave the shell entirely, land the page on the server's origin, and
 * put an identity provider's login form somewhere without an address bar. The
 * host runs the flow in the system browser instead and stores the session
 * where page script cannot read it.
 *
 * This module is the seam between the two: the host installs a sign-in
 * function when it connects, and the gate offers it instead of the navigation
 * link. The host also announces session changes it makes on its own — the
 * tray menu can sign in and out without the page asking — through a global
 * this module installs, the same way the stream arrives.
 *
 * In a browser nothing is installed and everything here stays inert.
 */

const DISPATCH_GLOBAL = '__PROMVIEW_SESSION__';

export type HostSessionMessage = { kind: 'signedIn' } | { kind: 'signedOut' };

type Invoke = (command: string, payload?: unknown) => Promise<unknown>;

interface SessionGlobals {
  [DISPATCH_GLOBAL]?: (message: HostSessionMessage) => void;
}

let signIn: (() => Promise<void>) | undefined;

const listeners = new Set<(message: HostSessionMessage) => void>();

/**
 * The host's sign-in flow, or undefined in a browser. The gate decides what to
 * render by whether this exists: a button that asks the host, or the
 * navigation link that works everywhere else.
 */
export function getHostSignIn(): (() => Promise<void>) | undefined {
  return signIn;
}

/**
 * Subscribes to session changes the host announces. Returns the unsubscribe,
 * in the shape a React effect wants to return.
 */
export function onHostSessionChange(listener: (message: HostSessionMessage) => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

/**
 * Installs the sign-in seam and the dispatch global. Called once when a host
 * is detected; a browser never reaches it.
 */
export function installHostSession(invoke: Invoke): void {
  signIn = async () => {
    // Resolves once the session is stored, not once the browser opens: the
    // caller can re-check the session the moment this settles.
    await invoke('sign_in');
  };
  (globalThis as SessionGlobals)[DISPATCH_GLOBAL] = (message: HostSessionMessage) => {
    for (const listener of listeners) {
      listener(message);
    }
  };
}

/** Test-only: puts the module back the way a browser finds it. */
export function resetHostSession(): void {
  signIn = undefined;
  listeners.clear();
  delete (globalThis as SessionGlobals)[DISPATCH_GLOBAL];
}
