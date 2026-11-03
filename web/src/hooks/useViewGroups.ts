import { useCallback, useEffect, useState } from 'react';
import { fetchViewGroups, joinViewGroup, leaveViewGroup } from '../access/viewGroups';
import type { ViewGroup } from '../access/viewGroups';

export interface ViewGroupsState {
  groups: ViewGroup[];
  /** A join or leave is in flight; the controls wait for it. */
  pending: boolean;
  error: string | null;
  join: (name: string) => void;
  leave: (name: string) => void;
}

/**
 * The caller's view groups and the two things a user may do with them.
 *
 * Membership changes what the server derives the view from, so the list the
 * console already holds is stale the moment one lands. `onChanged` is how the
 * caller learns that, and refetches under the new view. It fires only after
 * the server has accepted the change, never on an optimistic guess, because a
 * refetch made first would read the membership from before.
 */
export function useViewGroups(enabled: boolean, onChanged: () => void): ViewGroupsState {
  const [groups, setGroups] = useState<ViewGroup[]>([]);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const reload = useCallback(() => {
    return fetchViewGroups()
      .then((loaded) => {
        setGroups(loaded);
        setError(null);
      })
      .catch((cause: unknown) => {
        setError(cause instanceof Error ? cause.message : String(cause));
      });
  }, []);

  useEffect(() => {
    if (!enabled) {
      return;
    }
    void reload();
  }, [enabled, reload]);

  const change = useCallback(
    (action: (name: string) => Promise<void>, name: string) => {
      setPending(true);
      setError(null);
      action(name)
        .then(() => reload())
        .then(() => onChanged())
        .catch((cause: unknown) => {
          // Reload first: a successful reload clears the error, and the
          // message is the reason the list may now differ from what the user
          // expected.
          const message = cause instanceof Error ? cause.message : String(cause);
          return reload().then(() => setError(message));
        })
        .finally(() => setPending(false));
    },
    [onChanged, reload],
  );

  const join = useCallback((name: string) => change(joinViewGroup, name), [change]);
  const leave = useCallback((name: string) => change(leaveViewGroup, name), [change]);

  return { groups: enabled ? groups : [], pending, error, join, leave };
}
