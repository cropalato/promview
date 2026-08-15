import { useCallback, useEffect, useRef, useState } from 'react';
import { ALERTS_PAGE_SIZE, fetchAlerts } from '../alerts/api';
import type { AlertsPage } from '../alerts/api';
import type { AlertSummary } from '../alerts/types';

/** Accumulated alert data: the rows loaded so far plus server-side totals. */
export interface AlertsData {
  alerts: AlertSummary[];
  total: number;
  severityCounts: Record<string, number>;
  /** Opaque cursor for the next page; empty when everything is loaded. */
  nextCursor: string;
  /** Resume position for the live event stream, from the latest page. */
  streamCursor: number;
}

export type AlertsState =
  | { status: 'loading' }
  | {
      status: 'ready';
      data: AlertsData;
      loadingMore: boolean;
      /** Pagination failure; loaded rows stay usable and the page retries. */
      moreError: Error | null;
    }
  | { status: 'error'; error: Error };

function toError(value: unknown): Error {
  return value instanceof Error ? value : new Error(String(value));
}

function toData(page: AlertsPage): AlertsData {
  return {
    alerts: page.alerts,
    total: page.total,
    severityCounts: page.severityCounts,
    nextCursor: page.nextCursor,
    streamCursor: page.streamCursor,
  };
}

/** Appends a page, dropping rows already loaded so re-fetches stay idempotent. */
function mergeAlerts(
  existing: readonly AlertSummary[],
  incoming: readonly AlertSummary[],
): AlertSummary[] {
  const seen = new Set(existing.map((alert) => alert.id));
  const merged = [...existing];
  for (const alert of incoming) {
    if (!seen.has(alert.id)) {
      seen.add(alert.id);
      merged.push(alert);
    }
  }
  return merged;
}

/** Debounce window for coalescing bursts of stream events into one refresh. */
export const LIVE_REFRESH_DEBOUNCE_MS = 750;

export interface UseAlertsOptions {
  /** Test hook: override the live-refresh debounce window. */
  liveRefreshDebounceMs?: number;
}

/**
 * Pages through `GET /api/v1/alerts` once `enabled` (i.e. the runtime config
 * has loaded). The first page lists firing alerts; `loadMore` follows the
 * server cursor until it is exhausted, and `retry` restarts from the first
 * page after an initial-load failure.
 *
 * `scheduleLiveRefresh` is the stream entry point: alert events funnel
 * through it into a debounced, coalesced first-page refetch that quietly
 * replaces the current rows/totals without dropping back to the loading
 * panel. Bursts collapse into at most one fetch per debounce window (plus a
 * single trailing refetch when events arrive while one is in flight).
 */
export function useAlerts(
  enabled: boolean,
  options: UseAlertsOptions = {},
): {
  state: AlertsState;
  retry: () => void;
  loadMore: () => void;
  scheduleLiveRefresh: () => void;
} {
  const { liveRefreshDebounceMs = LIVE_REFRESH_DEBOUNCE_MS } = options;
  const [state, setState] = useState<AlertsState>({ status: 'loading' });
  const [attempt, setAttempt] = useState(0);
  const [pendingCursor, setPendingCursor] = useState<string | null>(null);
  const nextCursorRef = useRef('');
  const pageInFlightRef = useRef(false);
  const readyRef = useRef(false);
  const liveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const liveInFlightRef = useRef(false);
  const livePendingRef = useRef(false);
  const disposedRef = useRef(false);

  // First page: firing alerts, fetched once the shell is configured.
  useEffect(() => {
    if (!enabled) {
      return;
    }
    let cancelled = false;
    nextCursorRef.current = '';
    pageInFlightRef.current = false;
    setState({ status: 'loading' });

    fetchAlerts({ limit: ALERTS_PAGE_SIZE, status: 'firing' })
      .then((page) => {
        if (cancelled) {
          return;
        }
        nextCursorRef.current = page.nextCursor;
        setState({ status: 'ready', data: toData(page), loadingMore: false, moreError: null });
      })
      .catch((error: unknown) => {
        if (!cancelled) {
          setState({ status: 'error', error: toError(error) });
        }
      });

    return () => {
      cancelled = true;
    };
  }, [enabled, attempt]);

  // Subsequent pages, driven by the cursor pending in state.
  useEffect(() => {
    if (pendingCursor === null) {
      return;
    }
    let cancelled = false;
    setState((current) =>
      current.status === 'ready' ? { ...current, loadingMore: true, moreError: null } : current,
    );

    fetchAlerts({ limit: ALERTS_PAGE_SIZE, status: 'firing', cursor: pendingCursor })
      .then((page) => {
        if (cancelled) {
          return;
        }
        nextCursorRef.current = page.nextCursor;
        setState((current) => {
          if (current.status !== 'ready') {
            return current;
          }
          return {
            ...current,
            loadingMore: false,
            data: {
              alerts: mergeAlerts(current.data.alerts, page.alerts),
              total: page.total,
              severityCounts: page.severityCounts,
              nextCursor: page.nextCursor,
              streamCursor: page.streamCursor,
            },
          };
        });
      })
      .catch((error: unknown) => {
        if (!cancelled) {
          setState((current) =>
            current.status === 'ready'
              ? { ...current, loadingMore: false, moreError: toError(error) }
              : current,
          );
        }
      })
      .finally(() => {
        pageInFlightRef.current = false;
        if (!cancelled) {
          setPendingCursor(null);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [pendingCursor]);

  // Track readiness in a ref so the stable live-refresh callbacks below can
  // gate on it without capturing state.
  useEffect(() => {
    readyRef.current = state.status === 'ready';
  }, [state]);

  // Unmount: cancel a pending live-refresh timer and mute in-flight work.
  useEffect(
    () => () => {
      disposedRef.current = true;
      if (liveTimerRef.current !== null) {
        clearTimeout(liveTimerRef.current);
        liveTimerRef.current = null;
      }
    },
    [],
  );

  // Quiet first-page refetch: replaces rows/totals in place so the console
  // never falls back to the initial loading panel during live updates.
  const runLiveRefresh = useCallback((): void => {
    if (liveInFlightRef.current) {
      livePendingRef.current = true;
      return;
    }
    liveInFlightRef.current = true;
    fetchAlerts({ limit: ALERTS_PAGE_SIZE, status: 'firing' })
      .then((page) => {
        if (disposedRef.current) {
          return;
        }
        nextCursorRef.current = page.nextCursor;
        setState((current) =>
          current.status === 'ready' ? { ...current, data: toData(page) } : current,
        );
      })
      .catch(() => {
        // Quiet refresh: keep the stale rows; the next stream event retries.
      })
      .finally(() => {
        liveInFlightRef.current = false;
        if (!disposedRef.current && livePendingRef.current) {
          livePendingRef.current = false;
          scheduleRef.current();
        }
      });
  }, []);

  const scheduleLiveRefresh = useCallback((): void => {
    if (!readyRef.current || liveTimerRef.current !== null) {
      return;
    }
    liveTimerRef.current = setTimeout(() => {
      liveTimerRef.current = null;
      runLiveRefresh();
    }, liveRefreshDebounceMs);
  }, [liveRefreshDebounceMs, runLiveRefresh]);

  const scheduleRef = useRef(scheduleLiveRefresh);
  useEffect(() => {
    scheduleRef.current = scheduleLiveRefresh;
  }, [scheduleLiveRefresh]);

  const retry = useCallback(() => setAttempt((current) => current + 1), []);

  const loadMore = useCallback(() => {
    if (pageInFlightRef.current || nextCursorRef.current === '') {
      return;
    }
    pageInFlightRef.current = true;
    setPendingCursor(nextCursorRef.current);
  }, []);

  return { state, retry, loadMore, scheduleLiveRefresh };
}
