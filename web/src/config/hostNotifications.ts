import type {
  NotificationFactoryLike,
  NotificationHandle,
  ShowNotificationOptions,
} from '../notifications/notifier';

/**
 * Notifications shown by the host rather than the webview.
 *
 * Not an optimisation: WebKitGTK has no usable Notification API, so in a shell
 * built on it the console's own notifications never appear at all. Routing
 * through the host is what makes them exist.
 *
 * Only the *showing* moves. Whether to notify stays in the console, which owns
 * the opt-in, the label selector, and the ledger that stops a replayed event
 * notifying twice — the same reasoning that kept reconnect policy there when
 * the stream moved.
 */

type Invoke = (command: string, payload?: unknown) => Promise<unknown>;

/**
 * A host is the application itself, so its permission to notify is the
 * operating system's business and was settled at install time. There is nothing
 * to prompt for and nothing that can be denied from here.
 */
const HOST_PERMISSION = 'granted' as NotificationPermission;

export function createHostNotificationFactory(invoke: Invoke): NotificationFactoryLike {
  return {
    get permission() {
      return HOST_PERMISSION;
    },
    requestPermission: () => Promise.resolve(HOST_PERMISSION),
    create: (title: string, options: ShowNotificationOptions): NotificationHandle => {
      // Wrapped rather than chained directly: the caller supplies the invoke,
      // and a host that returns anything other than a promise must not throw
      // from inside a stream handler.
      void Promise.resolve(
        invoke('show_notification', {
          title,
          body: options.body ?? '',
          // The host's own filter matches on these. Sent even when it has no
          // rules: a host that cannot see the fields could only filter on the
          // rendered title, which is not a selector anyone can reason about.
          labels: options.labels ?? {},
        }),
      ).catch((error: unknown) => {
        // A notification that fails to appear must not take the stream handler
        // down with it; the alert is in the console either way. It is still
        // reported: silence is exactly how this failed before the host returned
        // the error at all.
        console.error('promview: the host could not show a notification', error);
      });
      // The host has no click callback to offer yet, so this handle exists to
      // satisfy the console's contract. Deep-linking from a notification is
      // still to come; `onclick` simply never fires.
      return {
        onclick: null,
        close() {},
      };
    },
  };
}
