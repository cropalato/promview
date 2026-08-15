import { useCallback, useEffect, useState } from 'react';
import { loadRuntimeConfig } from '../config/runtimeConfig';
import type { RuntimeConfig } from '../config/runtimeConfig';

export type RuntimeConfigState =
  | { status: 'loading' }
  | { status: 'ready'; config: RuntimeConfig }
  | { status: 'error'; error: Error };

function toError(value: unknown): Error {
  return value instanceof Error ? value : new Error(String(value));
}

/**
 * Loads `/api/v1/config` once on mount and exposes a retry that re-issues the
 * request. The load is cancelled safely if the component unmounts in flight.
 */
export function useRuntimeConfig(): { state: RuntimeConfigState; retry: () => void } {
  const [state, setState] = useState<RuntimeConfigState>({ status: 'loading' });
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setState({ status: 'loading' });

    loadRuntimeConfig()
      .then((config) => {
        if (!cancelled) {
          setState({ status: 'ready', config });
        }
      })
      .catch((error: unknown) => {
        if (!cancelled) {
          setState({ status: 'error', error: toError(error) });
        }
      });

    return () => {
      cancelled = true;
    };
  }, [attempt]);

  const retry = useCallback(() => setAttempt((current) => current + 1), []);

  return { state, retry };
}
