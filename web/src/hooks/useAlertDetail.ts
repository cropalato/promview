import { useCallback, useEffect, useRef, useState } from 'react';
import { isAlertsUnauthorized } from '../alerts/api';
import {
  addAlertNote,
  fetchAlertDetail,
  isAlertNotFound,
  setAlertAcknowledgement,
  setAlertAssignee,
  setAlertClosed,
} from '../alerts/detail';
import type { AlertDetailResult } from '../alerts/detail';

export type AlertDetailState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'ready'; detail: AlertDetailResult }
  | { status: 'error'; error: Error }
  | { status: 'not-found' };

function toError(value: unknown): Error {
  return value instanceof Error ? value : new Error(String(value));
}

export interface UseAlertDetailOptions {
  /**
   * Called when a detail request is rejected with HTTP 401 — the session
   * expired after boot, and the shell drops back to the sign-in gate.
   */
  onUnauthorized?: () => void;
}

/**
 * Loads `GET /api/v1/alerts/{id}` for the currently selected alert. Changing
 * the id restarts from the loading state so a stale alert is never shown for
 * a different selection; `retry` re-runs the request after an error or a
 * not-found.
 *
 * `refreshIfSelected` is the live-stream entry point: when a stream event
 * targets the open alert it quietly refetches and replaces the detail in
 * place (never dropping back to the loading panel), mirroring the list's
 * quiet refresh. Events for other alerts — or failures — are ignored; the
 * stale detail stays put and the next event retries.
 *
 * `acknowledge` is the operator-action entry point: it posts the toggle and,
 * on success, replaces the ready detail with the response (the endpoint
 * returns the updated alert plus the refreshed history). Failures keep the
 * current detail and rethrow so the caller can show inline feedback; a 401
 * also reports session expiry like any other request.
 */
export function useAlertDetail(
  alertId: string | null,
  options: UseAlertDetailOptions = {},
): {
  state: AlertDetailState;
  retry: () => void;
  refreshIfSelected: (id: string) => void;
  acknowledge: (acknowledged: boolean) => Promise<void>;
  /** Records who owns the alert; an empty string clears it. */
  assign: (assignee: string) => Promise<void>;
  /** Files the alert as handled, or reopens it. */
  close: (closed: boolean) => Promise<void>;
  /** Appends one operator note. */
  addNote: (body: string) => Promise<void>;
} {
  const { onUnauthorized } = options;
  const [state, setState] = useState<AlertDetailState>(() =>
    alertId === null ? { status: 'idle' } : { status: 'loading' },
  );
  const [attempt, setAttempt] = useState(0);
  // A new selection or a retry restarts from the loading state during render,
  // so a stale alert is never painted for the new id.
  const [request, setRequest] = useState({ alertId, attempt });
  if (request.alertId !== alertId || request.attempt !== attempt) {
    setRequest({ alertId, attempt });
    setState(alertId === null ? { status: 'idle' } : { status: 'loading' });
  }
  const alertIdRef = useRef(alertId);
  const readyRef = useRef(false);
  const refreshInFlightRef = useRef(false);
  const refreshPendingRef = useRef(false);
  const disposedRef = useRef(false);

  const onUnauthorizedRef = useRef(onUnauthorized);
  useEffect(() => {
    onUnauthorizedRef.current = onUnauthorized;
  }, [onUnauthorized]);

  const reportIfUnauthorized = useCallback((error: unknown): void => {
    if (isAlertsUnauthorized(error)) {
      onUnauthorizedRef.current?.();
    }
  }, []);

  useEffect(() => {
    alertIdRef.current = alertId;
  }, [alertId]);

  useEffect(() => {
    readyRef.current = state.status === 'ready';
  }, [state]);

  useEffect(() => {
    if (alertId === null) {
      return;
    }
    let cancelled = false;

    fetchAlertDetail(alertId)
      .then((detail) => {
        if (!cancelled) {
          setState({ status: 'ready', detail });
        }
      })
      .catch((error: unknown) => {
        if (cancelled) {
          return;
        }
        reportIfUnauthorized(error);
        if (isAlertNotFound(error)) {
          setState({ status: 'not-found' });
        } else {
          setState({ status: 'error', error: toError(error) });
        }
      });

    return () => {
      cancelled = true;
    };
  }, [alertId, attempt, reportIfUnauthorized]);

  // Unmount: mute in-flight work.
  useEffect(
    () => () => {
      disposedRef.current = true;
    },
    [],
  );

  const runQuietRefresh = useCallback(
    (target: string): void => {
      const run = (id: string): void => {
        if (refreshInFlightRef.current) {
          refreshPendingRef.current = true;
          return;
        }
        refreshInFlightRef.current = true;
        fetchAlertDetail(id)
          .then((detail) => {
            if (disposedRef.current) {
              return;
            }
            // Replace only when the drawer is still showing this alert.
            setState((current) =>
              current.status === 'ready' && alertIdRef.current === id
                ? { status: 'ready', detail }
                : current,
            );
          })
          .catch((error: unknown) => {
            // Quiet refresh: keep the stale detail; the next stream event retries.
            // A 401 means the session expired — route back to the sign-in gate.
            reportIfUnauthorized(error);
          })
          .finally(() => {
            refreshInFlightRef.current = false;
            if (!disposedRef.current && refreshPendingRef.current) {
              refreshPendingRef.current = false;
              const currentId = alertIdRef.current;
              if (currentId !== null && readyRef.current) {
                run(currentId);
              }
            }
          });
      };
      run(target);
    },
    [reportIfUnauthorized],
  );

  const refreshIfSelected = useCallback(
    (id: string): void => {
      if (!readyRef.current || alertIdRef.current !== id) {
        return;
      }
      runQuietRefresh(id);
    },
    [runQuietRefresh],
  );

  const retry = useCallback(() => setAttempt((current) => current + 1), []);

  const acknowledge = useCallback(
    async (acknowledged: boolean): Promise<void> => {
      const id = alertIdRef.current;
      if (id === null || !readyRef.current) {
        throw new Error('Alert detail is not loaded');
      }
      try {
        const updated = await setAlertAcknowledgement(id, acknowledged);
        if (disposedRef.current) {
          return;
        }
        // Replace only when the drawer is still showing this alert.
        setState((current) =>
          current.status === 'ready' && alertIdRef.current === id
            ? { status: 'ready', detail: updated }
            : current,
        );
      } catch (error) {
        // A 401 means the session expired — route back to the sign-in gate.
        reportIfUnauthorized(error);
        throw error;
      }
    },
    [reportIfUnauthorized],
  );

  // assign, close and note differ from acknowledge only in which request they
  // make, so they share one runner rather than three copies of the same
  // replace-if-still-showing and expire-on-401 handling.
  const runMutation = useCallback(
    async (mutate: (id: string) => Promise<AlertDetailResult>): Promise<void> => {
      const id = alertIdRef.current;
      if (id === null || !readyRef.current) {
        throw new Error('Alert detail is not loaded');
      }
      try {
        const updated = await mutate(id);
        if (disposedRef.current) {
          return;
        }
        setState((current) =>
          current.status === 'ready' && alertIdRef.current === id
            ? { status: 'ready', detail: updated }
            : current,
        );
      } catch (error) {
        reportIfUnauthorized(error);
        throw error;
      }
    },
    [reportIfUnauthorized],
  );

  const assign = useCallback(
    (assignee: string): Promise<void> => runMutation((id) => setAlertAssignee(id, assignee)),
    [runMutation],
  );

  const close = useCallback(
    (closed: boolean): Promise<void> => runMutation((id) => setAlertClosed(id, closed)),
    [runMutation],
  );

  const addNote = useCallback(
    (body: string): Promise<void> => runMutation((id) => addAlertNote(id, body)),
    [runMutation],
  );

  return { state, retry, refreshIfSelected, acknowledge, assign, close, addNote };
}
