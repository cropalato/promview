import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { AlertStreamEvent } from '../alerts/stream';
import { createAlertNotifier, resolveNotificationFactory } from '../notifications/notifier';
import type { NotificationFactoryLike, NotificationOptInState } from '../notifications/notifier';
import { createSeenEventStore } from '../notifications/store';
import type { StorageLike } from '../notifications/store';
import type { NotificationMatcher } from '../preferences/store';

export type { NotificationOptInState } from '../notifications/notifier';

export interface UseAlertNotificationsOptions {
  /** Navigation seam for notification clicks: `/alerts/{alertId}`. */
  navigateToAlert: (alertId: string) => void;
  /** Notification constructor seam; defaults to the browser global. */
  factory?: NotificationFactoryLike;
  /** Storage seam for the dedupe ledger, which stays per device. */
  storage?: StorageLike;
  focusWindow?: () => void;
  isDocumentHidden?: () => boolean;
  /**
   * The operator's stored opt-in and selector. These live with the rest of
   * their preferences, on the server where there is a user to key them
   * against, so the policy follows them to whatever client they sign in from
   * rather than to one browser profile.
   */
  enabled: boolean;
  matchers: readonly NotificationMatcher[];
  /**
   * Persists a change to the opt-in. Permission is the browser's business and
   * stays here; whether the operator wants notifications is not.
   */
  onEnabledChange: (enabled: boolean) => void;
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
 * live permission state and a stable notifier that stream events flow through.
 *
 * The opt-in and selector are not owned here: they are the operator's stored
 * preferences, which follow them between clients. Permission is the opposite —
 * it belongs to this browser profile and this origin, and no server can grant
 * it. The dedupe ledger stays local too, since it records what this device
 * already showed.
 */
export function useAlertNotifications({
  navigateToAlert,
  factory,
  storage,
  focusWindow,
  isDocumentHidden,
  enabled,
  matchers,
  onEnabledChange,
}: UseAlertNotificationsOptions): AlertNotifications {
  // Resolved once per mount: an injected factory wins, otherwise the
  // browser global when it exists. Permission itself is read live.
  const [resolvedFactory] = useState<NotificationFactoryLike | undefined>(
    () => factory ?? resolveNotificationFactory(),
  );
  const [permission, setPermission] = useState<NotificationPermission | 'unsupported'>(
    () => resolvedFactory?.permission ?? 'unsupported',
  );
  const preferenceEnabled = enabled;

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
      onEnabledChange(!preferenceEnabled);
      return;
    }
    if (current === 'default') {
      void resolvedFactory.requestPermission().then(
        (result) => {
          setPermission(result);
          if (result === 'granted') {
            onEnabledChange(true);
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
  }, [resolvedFactory, preferenceEnabled, onEnabledChange]);

  const navigateRef = useRef(navigateToAlert);
  useEffect(() => {
    navigateRef.current = navigateToAlert;
  }, [navigateToAlert]);

  const enabledRef = useRef(optInState === 'enabled');
  useEffect(() => {
    enabledRef.current = optInState === 'enabled';
  }, [optInState]);

  // Read through a ref so editing the selector takes effect on the next event
  // rather than rebuilding the notifier and losing its identity.
  const matchersRef = useRef(matchers);
  useEffect(() => {
    matchersRef.current = matchers;
  }, [matchers]);

  const notifier = useMemo(
    () =>
      createAlertNotifier({
        isEnabled: () => enabledRef.current,
        matchers: () => matchersRef.current,
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
