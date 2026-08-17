import { useCallback, useEffect, useState } from 'react';
import { SessionError, endSession, loadSession } from '../auth/session';
import type { NavigateTo, SessionFetch, SessionInfo } from '../auth/session';
import type { AuthMode } from '../config/runtimeConfig';

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
 * Resolves the session for protected deployments. Only OIDC mode calls
 * `/api/v1/me`: open mode keeps its anonymous viewer without an extra
 * request. In OIDC mode the state gates the console — alerts and the live
 * stream must not start until the state is `ready`:
 *
 * - `unauthenticated` (401) → sign-in link to the OIDC login endpoint;
 * - `forbidden` (403) → access-denied panel with a sign-out escape;
 * - `error` → retryable session check.
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
  authMode: AuthMode | undefined,
  deps: SessionDeps = {},
): {
  state: SessionState;
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
    if (authMode !== 'oidc') {
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
  }, [authMode, attempt, fetchImpl]);

  const retry = useCallback(() => setAttempt((current) => current + 1), []);

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

  return { state, retry, signOut, signOutState, expire };
}
