import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { AlertStreamEvent } from '../alerts/stream';
import { browserNotificationFactory, createAlertNotifier } from '../notifications/notifier';
import type { NotificationFactoryLike, NotificationOptInState } from '../notifications/notifier';
import {
  createSeenEventStore,
  loadNotificationPreference,
  saveNotificationPreference,
} from '../notifications/store';
import type { StorageLike } from '../notifications/store';

export type { NotificationOptInState } from '../notifications/notifier';

export interface UseAlertNotificationsOptions {
  /** Navigation seam for notification clicks: `/alerts/{alertId}`. */
  navigateToAlert: (alertId: string) => void;
  /** Notification constructor seam; defaults to the browser global. */
  factory?: NotificationFactoryLike;
  /** Storage seam for the preference and dedupe ledger. */
  storage?: StorageLike;
  focusWindow?: () => void;
  isDocumentHidden?: () => boolean;
}

export interface AlertNotifications {
  optInState: NotificationOptInState;
  /**
   * Top bar click handler. The browser permission prompt is only ever
   * requested here — on an explicit click — never on load or stream events.
   * Denied and unsupported states are inert: the browser will not show a
   * prompt for them anyway.
   */
  toggleOptIn: () => void;
  /** Stream event entry point; applies the notification criteria. */
  handleEvent: (event: AlertStreamEvent) => void;
}

/**
 * Owns the browser-notification opt-in for the console: the persisted
 * preference, the live permission state, and a stable notifier that stream
 * events flow through. The preference persists in localStorage; the dedupe
 * ledger lives there too, so replays after a reload never re-notify.
 */
export function useAlertNotifications({
  navigateToAlert,
  factory,
  storage,
  focusWindow,
  isDocumentHidden,
}: UseAlertNotificationsOptions): AlertNotifications {
  // Resolved once per mount: an injected factory wins, otherwise the
  // browser global when it exists. Permission itself is read live.
  const [resolvedFactory] = useState<NotificationFactoryLike | undefined>(
    () => factory ?? browserNotificationFactory(),
  );
  const [permission, setPermission] = useState<NotificationPermission | 'unsupported'>(
    () => resolvedFactory?.permission ?? 'unsupported',
  );
  const [preferenceEnabled, setPreferenceEnabled] = useState(() =>
    loadNotificationPreference(storage),
  );

  const optInState: NotificationOptInState =
    permission === 'unsupported'
      ? 'unsupported'
      : permission === 'denied'
        ? 'denied'
        : preferenceEnabled && permission === 'granted'
          ? 'enabled'
          : 'disabled';

  const toggleOptIn = useCallback(() => {
    if (resolvedFactory === undefined) {
      return;
    }
    // Re-read live permission on every click: it may have changed in the
    // browser's site settings while the console was open.
    const current = resolvedFactory.permission;
    setPermission(current);
    if (current === 'granted') {
      const next = !preferenceEnabled;
      setPreferenceEnabled(next);
      saveNotificationPreference(next, storage);
      return;
    }
    if (current === 'default') {
      void resolvedFactory.requestPermission().then(
        (result) => {
          setPermission(result);
          if (result === 'granted') {
            setPreferenceEnabled(true);
            saveNotificationPreference(true, storage);
          }
        },
        () => {
          // A rejected prompt (browser error or a dismissal the platform
          // reports as a failure) must never enable notifications and must
          // not surface as an unhandled rejection; refresh from live state
          // so a later click can retry.
          setPermission(resolvedFactory.permission);
        },
      );
    }
    // denied: no prompt is possible; the top bar shows the blocked state.
  }, [resolvedFactory, preferenceEnabled, storage]);

  const navigateRef = useRef(navigateToAlert);
  useEffect(() => {
    navigateRef.current = navigateToAlert;
  }, [navigateToAlert]);

  const enabledRef = useRef(optInState === 'enabled');
  useEffect(() => {
    enabledRef.current = optInState === 'enabled';
  }, [optInState]);

  const notifier = useMemo(
    () =>
      createAlertNotifier({
        isEnabled: () => enabledRef.current,
        isDocumentHidden: () =>
          isDocumentHidden !== undefined ? isDocumentHidden() : document.hidden,
        createNotification: (title, options) => {
          if (resolvedFactory === undefined) {
            // Unreachable while isEnabled() is accurate; stay defensive.
            throw new Error('browser notifications are not supported');
          }
          return resolvedFactory.create(title, options);
        },
        navigateToAlert: (alertId) => {
          navigateRef.current(alertId);
        },
        focusWindow: focusWindow ?? (() => window.focus()),
        store: createSeenEventStore(storage),
      }),
    [resolvedFactory, storage, focusWindow, isDocumentHidden],
  );

  const handleEvent = useCallback(
    (event: AlertStreamEvent) => {
      notifier.handleEvent(event);
    },
    [notifier],
  );

  return { optInState, toggleOptIn, handleEvent };
}
