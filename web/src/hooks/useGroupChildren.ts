import { useCallback, useRef, useState } from 'react';
import { ALERTS_PAGE_SIZE, fetchAlerts, isAlertsUnauthorized } from '../alerts/api';
import type { AlertsQuery } from '../alerts/api';
import type { AlertSummary } from '../alerts/types';

/**
 * Loads the members of expanded groups.
 *
 * A group's members are the ordinary alerts query with one matcher per grouping
 * key, so expanding reuses the same endpoint, cursor and access rules as the
 * flat list — there is no second way to read an alert.
 *
 * Collapsing discards what was loaded rather than caching it. Re-expanding
 * costs one request and always shows current data, which matters more here than
 * saving a fetch: a console that shows stale members inside a group is worse
 * than one that pauses briefly.
 */

export interface GroupChildren {
  alerts: AlertSummary[];
  nextCursor: string;
  total: number;
  loading: boolean;
  error: Error | null;
}

const EMPTY: GroupChildren = {
  alerts: [],
  nextCursor: '',
  total: 0,
  loading: true,
  error: null,
};

/** Serialises a group's key into the matchers that select its members. */
export function groupMatchers(key: Record<string, string>): string[] {
  return Object.entries(key)
    .filter(([name]) => name !== 'source')
    .map(([name, value]) => `${name}=${value}`);
}

/** The group key entries the endpoint takes as parameters rather than matchers. */
export function groupQuery(key: Record<string, string>): Pick<AlertsQuery, 'source'> {
  return key.source === undefined ? {} : { source: key.source };
}

export function groupId(key: Record<string, string>): string {
  return JSON.stringify(key);
}

export function useGroupChildren(
  query: AlertsQuery,
  options: { onUnauthorized?: () => void } = {},
): {
  children: Record<string, GroupChildren>;
  expand: (key: Record<string, string>) => void;
  collapse: (key: Record<string, string>) => void;
  loadMore: (key: Record<string, string>) => void;
  reset: () => void;
} {
  const [children, setChildren] = useState<Record<string, GroupChildren>>({});
  const { onUnauthorized } = options;
  const unauthorizedRef = useRef(onUnauthorized);
  unauthorizedRef.current = onUnauthorized;
  const queryRef = useRef(query);
  queryRef.current = query;

  const request = useCallback((key: Record<string, string>, cursor: string) => {
    const id = groupId(key);
    const base = queryRef.current;
    void fetchAlerts({
      ...base,
      ...groupQuery(key),
      match: [...(base.match ?? []), ...groupMatchers(key)],
      limit: ALERTS_PAGE_SIZE,
      ...(cursor === '' ? {} : { cursor }),
    })
      .then((page) => {
        setChildren((current) => {
          const existing = current[id];
          if (existing === undefined) {
            // Collapsed while the request was in flight; drop the result.
            return current;
          }
          const seen = new Set(existing.alerts.map((alert) => alert.id));
          const appended = page.alerts.filter((alert) => !seen.has(alert.id));
          return {
            ...current,
            [id]: {
              alerts: cursor === '' ? page.alerts : [...existing.alerts, ...appended],
              nextCursor: page.nextCursor,
              total: page.total,
              loading: false,
              error: null,
            },
          };
        });
      })
      .catch((error: unknown) => {
        if (isAlertsUnauthorized(error)) {
          unauthorizedRef.current?.();
          return;
        }
        setChildren((current) => {
          const existing = current[id];
          if (existing === undefined) {
            return current;
          }
          return { ...current, [id]: { ...existing, loading: false, error: error as Error } };
        });
      });
  }, []);

  const expand = useCallback(
    (key: Record<string, string>) => {
      const id = groupId(key);
      setChildren((current) => (id in current ? current : { ...current, [id]: EMPTY }));
      request(key, '');
    },
    [request],
  );

  const collapse = useCallback((key: Record<string, string>) => {
    const id = groupId(key);
    setChildren((current) => {
      if (!(id in current)) {
        return current;
      }
      const next = { ...current };
      delete next[id];
      return next;
    });
  }, []);

  const loadMore = useCallback(
    (key: Record<string, string>) => {
      const id = groupId(key);
      setChildren((current) => {
        const existing = current[id];
        if (existing === undefined || existing.nextCursor === '' || existing.loading) {
          return current;
        }
        request(key, existing.nextCursor);
        return { ...current, [id]: { ...existing, loading: true, error: null } };
      });
    },
    [request],
  );

  const reset = useCallback(() => setChildren({}), []);

  return { children, expand, collapse, loadMore, reset };
}
