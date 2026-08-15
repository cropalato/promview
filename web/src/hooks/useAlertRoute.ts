import { useCallback, useEffect, useRef, useState } from 'react';

/**
 * Deep-linkable alert selection driven by the browser history. Selecting a
 * row pushes `/alerts/{id}`; closing the drawer replaces it with `/`; back
 * and forward navigation re-parse the location on `popstate`. A full router
 * is deliberately not introduced for one route — this hook is the entire
 * routing surface and stays transport-neutral for the Tauri shell (which can
 * drive the same selection state without a browser history).
 */

/** Extracts the selected alert id from `/alerts/{id}` paths; null elsewhere. */
export function alertIdFromPath(pathname: string): string | null {
  const match = /^\/alerts\/([^/]+?)\/?$/.exec(pathname);
  const raw = match?.[1];
  if (raw === undefined || raw === '') {
    return null;
  }
  try {
    return decodeURIComponent(raw);
  } catch {
    return raw;
  }
}

export function alertDetailPath(id: string): string {
  return `/alerts/${encodeURIComponent(id)}`;
}

export interface AlertRoute {
  /** The alert selected in the URL, or null when the list route is active. */
  selectedAlertId: string | null;
  /** Pushes `/alerts/{id}` so back navigation closes the detail view. */
  openAlert: (id: string) => void;
  /** Replaces the detail entry with `/` so back never reopens a closed view. */
  closeAlert: () => void;
}

export function useAlertRoute(): AlertRoute {
  const [selectedAlertId, setSelectedAlertId] = useState<string | null>(() =>
    alertIdFromPath(window.location.pathname),
  );
  const selectedRef = useRef(selectedAlertId);

  useEffect(() => {
    selectedRef.current = selectedAlertId;
  }, [selectedAlertId]);

  // Back/forward: the URL is the source of truth for the selection.
  useEffect(() => {
    const handlePopState = () => {
      setSelectedAlertId(alertIdFromPath(window.location.pathname));
    };
    window.addEventListener('popstate', handlePopState);
    return () => window.removeEventListener('popstate', handlePopState);
  }, []);

  const openAlert = useCallback((id: string) => {
    if (selectedRef.current === id) {
      return;
    }
    window.history.pushState(null, '', alertDetailPath(id));
    selectedRef.current = id;
    setSelectedAlertId(id);
  }, []);

  const closeAlert = useCallback(() => {
    if (selectedRef.current === null) {
      return;
    }
    window.history.replaceState(null, '', '/');
    selectedRef.current = null;
    setSelectedAlertId(null);
  }, []);

  return { selectedAlertId, openAlert, closeAlert };
}
