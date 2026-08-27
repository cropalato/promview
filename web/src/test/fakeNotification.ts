import type { NotificationHandle, ShowNotificationOptions } from '../notifications/notifier';

/**
 * Test double for the browser `Notification` global. Records every shown
 * notification so tests can assert titles/bodies, drive clicks manually,
 * and control the permission state and prompt outcome.
 */
export class FakeNotification implements NotificationHandle {
  static instances: FakeNotification[] = [];
  static permission: NotificationPermission = 'granted';
  static requestCount = 0;
  /** Outcome of the next requestPermission() call; defaults to current. */
  static nextRequestResult: NotificationPermission | undefined;
  /** When set, the next requestPermission() call rejects with this error. */
  static nextRequestError: unknown;

  readonly title: string;
  readonly body: string | undefined;
  readonly tag: string | undefined;
  readonly labels: Record<string, string> | undefined;
  onclick: (() => void) | null = null;
  closed = false;

  constructor(title: string, options: ShowNotificationOptions = {}) {
    this.title = title;
    this.body = options.body;
    this.tag = options.tag;
    this.labels = options.labels;
    FakeNotification.instances.push(this);
  }

  static requestPermission(): Promise<NotificationPermission> {
    FakeNotification.requestCount += 1;
    if (FakeNotification.nextRequestError !== undefined) {
      const error = FakeNotification.nextRequestError;
      FakeNotification.nextRequestError = undefined;
      return Promise.reject(error);
    }
    const result = FakeNotification.nextRequestResult ?? FakeNotification.permission;
    FakeNotification.nextRequestResult = undefined;
    FakeNotification.permission = result;
    return Promise.resolve(result);
  }

  static latest(): FakeNotification {
    const latest = FakeNotification.instances[FakeNotification.instances.length - 1];
    if (latest === undefined) {
      throw new Error('No FakeNotification has been created yet');
    }
    return latest;
  }

  click(): void {
    this.onclick?.();
  }

  close(): void {
    this.closed = true;
  }

  static reset(): void {
    FakeNotification.instances = [];
    FakeNotification.permission = 'granted';
    FakeNotification.requestCount = 0;
    FakeNotification.nextRequestResult = undefined;
    FakeNotification.nextRequestError = undefined;
  }
}
