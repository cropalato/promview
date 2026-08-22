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
import type { NotificationMatcher } from '../preferences/store';

/** What the console matched on before selectors were configurable. */
const DEFAULT_SELECTOR: readonly NotificationMatcher[] = [
  { name: 'severity', op: '=', value: 'critical' },
];
import type { AlertStreamEvent, AlertStreamNotificationEvent } from '../alerts/stream';
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
  /**
   * The operator's stored selector, read per event so a change applies without
   * rebuilding the notifier. Absent keeps the rule the console hardcoded
   * before selectors existed.
   */
  matchers?: () => readonly NotificationMatcher[];
}

export interface AlertNotifier {
  handleEvent: (event: AlertStreamEvent) => void;
}

function notificationTitle(event: AlertStreamNotificationEvent): string {
  return `${event.severity === '' ? 'Alert' : titleCase(event.severity)}: ${event.alertName}`;
}

function titleCase(value: string): string {
  return value.charAt(0).toUpperCase() + value.slice(1);
}

/**
 * The event fields a selector may match on. Deliberately the same four the
 * server accepts: a selector it stored must mean the same thing here, or an
 * operator debugging a missing page is comparing two different rules.
 */
function eventField(event: AlertStreamNotificationEvent, name: string): string | undefined {
  switch (name) {
    case 'severity':
      return normalizeSeverity(event.severity);
    case 'alertname':
      return event.alertName;
    case 'source':
      return event.source;
    case 'team':
      return event.team;
    default:
      return undefined;
  }
}

/**
 * Matchers are ANDed. An empty selector matches nothing rather than
 * everything: read as vacuous truth, switching notifications on would page for
 * every alert in the deployment.
 */
export function eventMatchesSelector(
  event: AlertStreamNotificationEvent,
  matchers: readonly NotificationMatcher[],
): boolean {
  if (matchers.length === 0) {
    return false;
  }
  return matchers.every((matcher) => {
    const actual = eventField(event, matcher.name);
    if (actual === undefined) {
      // A field this console cannot read cannot be satisfied; refusing is the
      // safe direction, and the server rejects such a selector anyway.
      return false;
    }
    return matcher.op === '=' ? actual === matcher.value : actual !== matcher.value;
  });
}

function notificationBody(event: AlertStreamNotificationEvent): string {
  const context = [event.source, event.team].filter((part) => part !== '').join(' · ');
  return [event.summary, context].filter((part) => part !== '').join('\n');
}

/**
 * Evaluates each stream event against the notification criteria: only
 * `alert.created` events with critical severity qualify. Redacted
 * `alert.removed` events can never notify — they carry no context to show,
 * and the type check turns them away before any criterion runs. Every
 * qualifying event id is recorded in the seen ledger up front — including
 * ones suppressed because the document was visible or notifications were
 * off — so a replayed event can never notify late. A notification is shown
 * only when opted in and the document is hidden; clicking it focuses the
 * window and navigates to the alert detail.
 */
export function createAlertNotifier(options: AlertNotifierOptions): AlertNotifier {
  return {
    handleEvent(event) {
      if (event.type !== 'alert.created') {
        return;
      }
      const matchers = options.matchers?.() ?? DEFAULT_SELECTOR;
      if (!eventMatchesSelector(event, matchers)) {
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
