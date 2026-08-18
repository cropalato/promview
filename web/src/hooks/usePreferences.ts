import { useCallback, useEffect, useRef, useState } from 'react';
import { loadPreferences, readLocalPreferences, savePreferences } from '../preferences/store';
import type { Preferences, PreferencesOrigin } from '../preferences/store';

/**
 * Holds the console's layout preferences.
 *
 * The browser copy is read synchronously on first render so the table draws in
 * the operator's own layout immediately, then the server copy replaces it when
 * it arrives. Saving goes back to wherever the load came from: the server when
 * there is a user to key against, the browser otherwise.
 */
export function usePreferences(
  enabled: boolean,
  serverBacked: boolean,
): {
  preferences: Preferences;
  origin: PreferencesOrigin;
  update: (next: Preferences) => void;
} {
  // Read synchronously so the table draws in the operator's own layout on the
  // first paint. It is gated on nothing: the console may still be locked, and
  // the browser copy is the only layout an open-mode deployment ever has.
  const [preferences, setPreferences] = useState<Preferences>(() => readLocalPreferences());
  const [origin, setOrigin] = useState<PreferencesOrigin>('local');
  const originRef = useRef<PreferencesOrigin>('local');

  useEffect(() => {
    // An open-mode deployment has no user to key a layout against, so the
    // endpoint would 404 on every boot. The browser copy is authoritative
    // there and the request is not worth making.
    if (!enabled || !serverBacked) {
      return;
    }
    let active = true;
    void loadPreferences().then((loaded) => {
      if (!active) {
        return;
      }
      setPreferences(loaded.preferences);
      setOrigin(loaded.origin);
      originRef.current = loaded.origin;
    });
    return () => {
      active = false;
    };
  }, [enabled, serverBacked]);

  const update = useCallback((next: Preferences) => {
    // Applied immediately: a layout change that waits for a round trip feels
    // broken, and the save cannot fail in a way the operator needs to act on.
    setPreferences(next);
    void savePreferences(next, originRef.current).then((saved) => {
      originRef.current = saved;
      setOrigin(saved);
    });
  }, []);

  return { preferences, origin, update };
}
