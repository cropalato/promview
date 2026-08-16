/**
 * Browser-notification dispatch for live alert events. Plain `Notification`
 * construction — no service worker: the console only notifies for events
 * arriving while the tab is already open but hidden, which the Page
 * Visibility check covers. Every decision input (permission/opt-in state,
 * document visibility, window focus, navigation, the constructor itself)
 * is an injected seam so tests and the future Tauri shell can drive the
 * same logic without a browser notification stack.
 */
import { normalizeSeverity } from '../alerts/severity';
import type { AlertStreamEvent } from '../alerts/stream';
import type { SeenEventStore } from './store';

/**
 * Effective opt-in shown in the top bar: `unsupported` when the browser has
 * no Notification API, `denied` when the user blocked permission, `enabled`
 * when the preference is on and permission is granted, else `disabled`.
 */
export type NotificationOptInState = 'unsupported' | 'denied' | 'enabled' | 'disabled';

/** Minimal handle on a shown notification; the factory adapts the DOM type. */
export interface NotificationHandle {
  onclick: (() => void) | null;
  close(): void;
}

export interface ShowNotificationOptions {
  body?: string;
  /** Collapses duplicate OS notifications for the same alert. */
  tag?: string;
}

/**
 * Seam over the browser `Notification` global: permission state, the
 * permission prompt, and construction. Referenced through a factory so the
 * real thing is never touched in environments without it.
 */
export interface NotificationFactoryLike {
  readonly permission: NotificationPermission;
  requestPermission(): Promise<NotificationPermission>;
  create(title: string, options: ShowNotificationOptions): NotificationHandle;
}

/**
 * Browser-backed factory, or undefined where the Notification API is
 * missing. The permission getter reads live state so changes made outside
 * the app (site settings) are picked up on the next interaction. The DOM
 * notification is adapted to `NotificationHandle` so its event-handler
 * typing never leaks into the notifier.
 */
export function browserNotificationFactory(): NotificationFactoryLike | undefined {
  if (typeof Notification === 'undefined') {
    return undefined;
  }
  return {
    get permission() {
      return Notification.permission;
    },
    requestPermission: () => Notification.requestPermission(),
    create: (title, options) => {
      const notification = new Notification(title, options);
      let clickHandler: (() => void) | null = null;
      notification.onclick = () => {
        clickHandler?.();
      };
      return {
        get onclick(): (() => void) | null {
          return clickHandler;
        },
        set onclick(handler: (() => void) | null) {
          clickHandler = handler;
        },
        close: () => {
          notification.close();
        },
      };
    },
  };
}

export interface AlertNotifierOptions {
  /** Effective opt-in: preference on AND permission granted. */
  isEnabled: () => boolean;
  /** Page Visibility seam: notifications only make sense while hidden. */
  isDocumentHidden: () => boolean;
  createNotification: (title: string, options: ShowNotificationOptions) => NotificationHandle;
  /** Navigation seam: click sends the console to `/alerts/{alertId}`. */
  navigateToAlert: (alertId: string) => void;
  focusWindow: () => void;
  store: SeenEventStore;
}

export interface AlertNotifier {
  handleEvent: (event: AlertStreamEvent) => void;
}

function notificationTitle(event: AlertStreamEvent): string {
  return `Critical: ${event.alertName}`;
}

function notificationBody(event: AlertStreamEvent): string {
  const context = [event.source, event.team].filter((part) => part !== '').join(' · ');
  return [event.summary, context].filter((part) => part !== '').join('\n');
}

/**
 * Evaluates each stream event against the notification criteria: only
 * `alert.created` events with critical severity qualify. Every qualifying
 * event id is recorded in the seen ledger up front — including ones
 * suppressed because the document was visible or notifications were off —
 * so a replayed event can never notify late. A notification is shown only
 * when opted in and the document is hidden; clicking it focuses the window
 * and navigates to the alert detail.
 */
export function createAlertNotifier(options: AlertNotifierOptions): AlertNotifier {
  return {
    handleEvent(event) {
      if (event.type !== 'alert.created') {
        return;
      }
      if (normalizeSeverity(event.severity) !== 'critical') {
        return;
      }
      const { store } = options;
      if (store.has(event.id)) {
        return;
      }
      store.add(event.id);
      if (!options.isEnabled() || !options.isDocumentHidden()) {
        return;
      }
      let notification: NotificationHandle;
      try {
        notification = options.createNotification(notificationTitle(event), {
          body: notificationBody(event),
          tag: `promview-alert-${event.alertId}`,
        });
      } catch {
        // Construction can still fail despite a granted-looking permission
        // (e.g. revoked between checks); never let it break the stream.
        return;
      }
      notification.onclick = () => {
        options.focusWindow();
        options.navigateToAlert(event.alertId);
        notification.close();
      };
    },
  };
}
