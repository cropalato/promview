import { useCallback, useEffect, useRef, useState } from 'react';
import { ALERTS_PAGE_SIZE, fetchAlerts, isAlertsUnauthorized } from '../alerts/api';
import type { AlertsPage, AlertsQuery } from '../alerts/api';
import type { AlertSummary } from '../alerts/types';

/**
 * Server-side query applied to every alerts request: serialized label
 * matchers (repeated `match` params) plus the sort field/order. The hook
 * restarts from the first page whenever the query content changes.
 */
export type AlertsQueryInput = Pick<AlertsQuery, 'match' | 'sort' | 'order'>;

/** Content key for a query; object identity is irrelevant to the hook. */
function queryKeyOf(query: AlertsQueryInput): string {
  return JSON.stringify([query.match ?? [], query.sort ?? null, query.order ?? null]);
}

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
  /**
   * Called when any alerts request is rejected with HTTP 401 — in protected
   * deployments the session expired after boot, and the shell drops back to
   * the sign-in gate instead of showing a generic request error.
   */
  onUnauthorized?: () => void;
}

/**
 * Pages through `GET /api/v1/alerts` once `enabled` (i.e. the runtime config
 * has loaded and, in protected deployments, the session is verified). The
 * first page lists firing alerts; `loadMore` follows the server cursor until
 * it is exhausted, and `retry` restarts from the first page after an
 * initial-load failure. When `enabled` goes false (session lost), loaded
 * rows, cursors, and pending refreshes are dropped so nothing stale shows or
 * streams when the console unlocks again.
 *
 * `query` carries the server-side filter (label matchers) and sort applied
 * to every request; when its content changes the hook drops back to the
 * first page and re-paginates from scratch, and any page request still in
 * flight from the previous query is discarded.
 *
 * `scheduleLiveRefresh` is the stream entry point: alert events funnel
 * through it into a debounced, coalesced first-page refetch that quietly
 * replaces the current rows/totals without dropping back to the loading
 * panel. Bursts collapse into at most one fetch per debounce window (plus a
 * single trailing refetch when events arrive while one is in flight).
 */
export function useAlerts(
  enabled: boolean,
  query: AlertsQueryInput = {},
  options: UseAlertsOptions = {},
): {
  state: AlertsState;
  retry: () => void;
  loadMore: () => void;
  scheduleLiveRefresh: () => void;
} {
  const { liveRefreshDebounceMs = LIVE_REFRESH_DEBOUNCE_MS, onUnauthorized } = options;
  const [state, setState] = useState<AlertsState>({ status: 'loading' });
  const [attempt, setAttempt] = useState(0);
  const [pendingPage, setPendingPage] = useState<{ cursor: string; key: string } | null>(null);
  const nextCursorRef = useRef('');
  const pageInFlightRef = useRef(false);
  const readyRef = useRef(false);
  const liveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const liveInFlightRef = useRef(false);
  const livePendingRef = useRef(false);
  const disposedRef = useRef(false);

  const queryKey = queryKeyOf(query);
  // Fetch closures read the latest query through this ref; `queryKey` drives
  // the effect dependencies so callers never have to memoize the object.
  const queryRef = useRef({ key: queryKey, query });
  useEffect(() => {
    queryRef.current = { key: queryKey, query };
  });

  const onUnauthorizedRef = useRef(onUnauthorized);
  useEffect(() => {
    onUnauthorizedRef.current = onUnauthorized;
  }, [onUnauthorized]);

  const reportIfUnauthorized = useCallback((error: unknown): void => {
    if (isAlertsUnauthorized(error)) {
      onUnauthorizedRef.current?.();
    }
  }, []);

  // First page: firing alerts, fetched once the shell is configured and
  // quietly re-fetched from scratch whenever the server-side query changes.
  useEffect(() => {
    if (!enabled) {
      // Auth gate closed (or shell not ready yet): reset to a clean loading
      // state and cancel any pending live refresh.
      nextCursorRef.current = '';
      pageInFlightRef.current = false;
      if (liveTimerRef.current !== null) {
        clearTimeout(liveTimerRef.current);
        liveTimerRef.current = null;
      }
      setState({ status: 'loading' });
      return;
    }
    let cancelled = false;
    nextCursorRef.current = '';
    pageInFlightRef.current = false;
    // A query change while ready keeps the current rows on screen and swaps
    // in the new first page when it lands; only the initial load (or a
    // retry after an error) drops back to the loading panel.
    setState((current) => (current.status === 'ready' ? current : { status: 'loading' }));

    fetchAlerts({ limit: ALERTS_PAGE_SIZE, status: 'firing', ...queryRef.current.query })
      .then((page) => {
        if (cancelled) {
          return;
        }
        nextCursorRef.current = page.nextCursor;
        setState({ status: 'ready', data: toData(page), loadingMore: false, moreError: null });
      })
      .catch((error: unknown) => {
        if (!cancelled) {
          reportIfUnauthorized(error);
          setState({ status: 'error', error: toError(error) });
        }
      });

    return () => {
      cancelled = true;
    };
  }, [enabled, attempt, queryKey, reportIfUnauthorized]);

  // Subsequent pages, driven by the cursor pending in state.
  useEffect(() => {
    if (pendingPage === null) {
      return;
    }
    if (pendingPage.key !== queryKey) {
      // The filter/sort changed after this page was requested; the fresh
      // first page supersedes the stale cursor.
      pageInFlightRef.current = false;
      setPendingPage(null);
      return;
    }
    let cancelled = false;
    setState((current) =>
      current.status === 'ready' ? { ...current, loadingMore: true, moreError: null } : current,
    );

    fetchAlerts({
      limit: ALERTS_PAGE_SIZE,
      status: 'firing',
      ...queryRef.current.query,
      cursor: pendingPage.cursor,
    })
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
          reportIfUnauthorized(error);
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
          setPendingPage(null);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [pendingPage, queryKey, reportIfUnauthorized]);

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
    fetchAlerts({ limit: ALERTS_PAGE_SIZE, status: 'firing', ...queryRef.current.query })
      .then((page) => {
        if (disposedRef.current) {
          return;
        }
        nextCursorRef.current = page.nextCursor;
        setState((current) =>
          current.status === 'ready' ? { ...current, data: toData(page) } : current,
        );
      })
      .catch((error: unknown) => {
        // Quiet refresh: keep the stale rows; the next stream event retries.
        // A 401 means the session expired — route back to the sign-in gate.
        reportIfUnauthorized(error);
      })
      .finally(() => {
        liveInFlightRef.current = false;
        if (!disposedRef.current && livePendingRef.current) {
          livePendingRef.current = false;
          scheduleRef.current();
        }
      });
  }, [reportIfUnauthorized]);

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
    setPendingPage({ cursor: nextCursorRef.current, key: queryRef.current.key });
  }, []);

  return { state, retry, loadMore, scheduleLiveRefresh };
}
