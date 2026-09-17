import { useCallback, useEffect, useState } from 'react';
import { SessionError, endSession, loadSession } from '../auth/session';
import type { NavigateTo, SessionFetch, SessionInfo } from '../auth/session';
import { onHostSessionChange } from '../config/hostSession';

export type SessionState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'ready'; session: SessionInfo }
  | { status: 'unauthenticated' }
  | { status: 'forbidden' }
  | { status: 'error'; error: Error };

export type SignOutState = 'idle' | 'pending' | 'error';

/** Transport/navigation overrides for tests and the future desktop client. */
export interface SessionDeps {
  fetchImpl?: SessionFetch;
  navigate?: NavigateTo;
}

function toError(value: unknown): Error {
  return value instanceof Error ? value : new Error(String(value));
}

/**
 * Resolves the session for every deployment.
 *
 * `/api/v1/me` is asked in all modes, open included: open mode answers 200
 * with the anonymous principal, so the console learns its own roles from the
 * server instead of assuming them. A mode-specific short-circuit here is
 * exactly the thing that would need editing again for the next auth mode.
 *
 * Whether the result gates is a separate question, and `requiresSignIn`
 * answers it: where a deployment demands a sign-in, `unauthenticated` is a
 * wall and `gated` stays true until a session is verified; where it does not,
 * the same state is just a resting state and the console loads anyway. The
 * gating states are:
 *
 * - `unauthenticated` (401) → the sign-in gate for the deployment's mode;
 * - `forbidden` (403) → access-denied panel with a sign-out escape;
 * - `error` → retryable session check.
 *
 * `undefined` means the runtime config has not arrived yet: nothing is asked,
 * because the answer would only race the config request.
 *
 * `signOut` revokes the server session and navigates home on success; a
 * failure keeps the session and flips `signOutState` to `error`.
 *
 * `expire` handles mid-session expiry: when an authenticated API request is
 * rejected with HTTP 401 after boot, it drops a verified session back to the
 * `unauthenticated` gate so the console stops alerting/streaming and shows
 * the sign-in state instead of a stale identity. It is a no-op in every
 * other state.
 */
export function useSession(
  requiresSignIn: boolean | undefined,
  deps: SessionDeps = {},
): {
  state: SessionState;
  /** Whether the console must stay paused until a session is verified. */
  gated: boolean;
  retry: () => void;
  signOut: () => void;
  signOutState: SignOutState;
  expire: () => void;
} {
  const { fetchImpl, navigate } = deps;
  const [state, setState] = useState<SessionState>({ status: 'idle' });
  const [attempt, setAttempt] = useState(0);
  const [signOutState, setSignOutState] = useState<SignOutState>('idle');

  useEffect(() => {
    if (requiresSignIn === undefined) {
      setState({ status: 'idle' });
      return;
    }
    let cancelled = false;
    setState({ status: 'loading' });

    loadSession(fetchImpl)
      .then((session) => {
        if (!cancelled) {
          setState({ status: 'ready', session });
        }
      })
      .catch((error: unknown) => {
        if (cancelled) {
          return;
        }
        if (error instanceof SessionError && error.status === 401) {
          setState({ status: 'unauthenticated' });
        } else if (error instanceof SessionError && error.status === 403) {
          setState({ status: 'forbidden' });
        } else {
          setState({ status: 'error', error: toError(error) });
        }
      });

    return () => {
      cancelled = true;
    };
  }, [requiresSignIn, attempt, fetchImpl]);

  const retry = useCallback(() => setAttempt((current) => current + 1), []);

  // A host shell can change the session without the page asking — its tray
  // menu signs in and out on its own. Signing in re-checks the session so the
  // console unlocks by itself; signing out drops a verified session back to
  // the gate the same way mid-session expiry does. In a browser nothing ever
  // arrives here.
  useEffect(
    () =>
      onHostSessionChange((message) => {
        if (message.kind === 'signedIn') {
          setAttempt((current) => current + 1);
        } else {
          setState((current) =>
            current.status === 'ready' ? { status: 'unauthenticated' } : current,
          );
        }
      }),
    [],
  );

  const signOut = useCallback(() => {
    setSignOutState('pending');
    endSession(fetchImpl, navigate)
      // Success navigates away and the app reboots signed out; nothing to do.
      .catch(() => {
        setSignOutState('error');
      });
  }, [fetchImpl, navigate]);

  const expire = useCallback(() => {
    setState((current) => (current.status === 'ready' ? { status: 'unauthenticated' } : current));
  }, []);

  // Only a deployment that demands a sign-in is held back by an unverified
  // session. Elsewhere the console has always loaded without one, and a failed
  // or refused /me must not take alerts down with it.
  const gated = requiresSignIn === true && state.status !== 'ready';

  return { state, gated, retry, signOut, signOutState, expire };
}
