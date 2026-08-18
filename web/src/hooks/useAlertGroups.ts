import { useCallback, useEffect, useRef, useState } from 'react';
import { ALERTS_PAGE_SIZE, fetchAlertGroups, isAlertsUnauthorized } from '../alerts/api';
import type { AlertGroupsPage, AlertsQuery } from '../alerts/api';

/**
 * Pages through the grouped shape of `GET /api/v1/alerts`.
 *
 * Deliberately a sibling of useAlerts rather than a branch inside it: the two
 * carry different page shapes, and folding both into one hook would mean every
 * caller narrowing a union on every read.
 */

export type AlertGroupsState =
  | { status: 'loading' }
  | { status: 'error'; error: Error }
  | {
      status: 'ready';
      data: AlertGroupsPage;
      loadingMore: boolean;
      moreError: Error | null;
    };

export interface UseAlertGroupsOptions {
  onUnauthorized?: () => void;
}

export function useAlertGroups(
  enabled: boolean,
  query: AlertsQuery,
  options: UseAlertGroupsOptions = {},
): {
  state: AlertGroupsState;
  retry: () => void;
  loadMore: () => void;
  refresh: () => void;
} {
  const { onUnauthorized } = options;
  const [state, setState] = useState<AlertGroupsState>({ status: 'loading' });
  const [attempt, setAttempt] = useState(0);
  const queryKey = JSON.stringify(query);
  const unauthorizedRef = useRef(onUnauthorized);
  const loadingMoreRef = useRef(false);

  useEffect(() => {
    unauthorizedRef.current = onUnauthorized;
  }, [onUnauthorized]);

  useEffect(() => {
    if (!enabled) {
      setState({ status: 'loading' });
      return;
    }
    let active = true;
    setState({ status: 'loading' });
    void fetchAlertGroups({ ...query, limit: ALERTS_PAGE_SIZE })
      .then((page) => {
        if (active) {
          setState({ status: 'ready', data: page, loadingMore: false, moreError: null });
        }
      })
      .catch((error: unknown) => {
        if (!active) {
          return;
        }
        if (isAlertsUnauthorized(error)) {
          unauthorizedRef.current?.();
          return;
        }
        setState({ status: 'error', error: error as Error });
      });
    return () => {
      active = false;
    };
    // queryKey stands in for query, which is rebuilt on every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled, queryKey, attempt]);

  const retry = useCallback(() => setAttempt((value) => value + 1), []);
  const refresh = retry;

  const loadMore = useCallback(() => {
    setState((current) => {
      if (current.status !== 'ready' || current.data.nextCursor === '' || loadingMoreRef.current) {
        return current;
      }
      loadingMoreRef.current = true;
      const cursor = current.data.nextCursor;
      void fetchAlertGroups({ ...query, limit: ALERTS_PAGE_SIZE, cursor })
        .then((page) => {
          setState((latest) => {
            if (latest.status !== 'ready') {
              return latest;
            }
            // Groups are keyed by their label values, which is what makes an
            // append idempotent if a page is ever delivered twice.
            const seen = new Set(latest.data.groups.map((group) => JSON.stringify(group.key)));
            const appended = page.groups.filter((group) => !seen.has(JSON.stringify(group.key)));
            return {
              status: 'ready',
              data: {
                ...page,
                groups: [...latest.data.groups, ...appended],
              },
              loadingMore: false,
              moreError: null,
            };
          });
        })
        .catch((error: unknown) => {
          if (isAlertsUnauthorized(error)) {
            unauthorizedRef.current?.();
            return;
          }
          setState((latest) =>
            latest.status === 'ready'
              ? { ...latest, loadingMore: false, moreError: error as Error }
              : latest,
          );
        })
        .finally(() => {
          loadingMoreRef.current = false;
        });
      return { ...current, loadingMore: true, moreError: null };
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [queryKey]);

  return { state, retry, loadMore, refresh };
}
