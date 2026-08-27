import { describe, expect, it, vi } from 'vitest';
import type { AlertStreamNotificationEvent, AlertStreamRemovedEvent } from '../alerts/stream';
import { FakeNotification } from '../test/fakeNotification';
import { createAlertNotifier, eventMatchesSelector } from './notifier';
import type { AlertNotifierOptions } from './notifier';
import { createSeenEventStore } from './store';
import type { StorageLike } from './store';

function memoryStorage(): StorageLike {
  const data = new Map<string, string>();
  return {
    getItem: (key) => data.get(key) ?? null,
    setItem: (key, value) => {
      data.set(key, value);
    },
    removeItem: (key) => {
      data.delete(key);
    },
  };
}

function createdCritical(
  overrides: Partial<AlertStreamNotificationEvent> = {},
): AlertStreamNotificationEvent {
  return {
    id: 8,
    type: 'alert.created',
    alertId: '42',
    occurredAt: '2026-08-14T12:00:00Z',
    severity: 'critical',
    alertName: 'HighErrorRate',
    summary: 'Error rate above 5% for 10m',
    source: 'am-eu',
    team: 'core',
    ...overrides,
  };
}

function removedEvent(overrides: Partial<AlertStreamRemovedEvent> = {}): AlertStreamRemovedEvent {
  return {
    id: 8,
    type: 'alert.removed',
    alertId: '42',
    occurredAt: '2026-08-14T12:00:00Z',
    ...overrides,
  };
}

interface Harness {
  options: AlertNotifierOptions;
  navigateToAlert: ReturnType<typeof vi.fn<(alertId: string) => void>>;
  focusWindow: ReturnType<typeof vi.fn<() => void>>;
  setEnabled: (enabled: boolean) => void;
  setHidden: (hidden: boolean) => void;
}

function harness(storage: StorageLike = memoryStorage()): Harness {
  FakeNotification.reset();
  const navigateToAlert = vi.fn<(alertId: string) => void>();
  const focusWindow = vi.fn<() => void>();
  let enabled = true;
  let hidden = true;
  const options: AlertNotifierOptions = {
    isEnabled: () => enabled,
    isDocumentHidden: () => hidden,
    createNotification: (title, notificationOptions) =>
      new FakeNotification(title, notificationOptions),
    navigateToAlert,
    focusWindow,
    store: createSeenEventStore(storage),
  };
  return {
    options,
    navigateToAlert,
    focusWindow,
    setEnabled: (next) => {
      enabled = next;
    },
    setHidden: (next) => {
      hidden = next;
    },
  };
}

describe('createAlertNotifier', () => {
  it('notifies for a new critical alert while opted in and hidden', () => {
    const { options } = harness();
    createAlertNotifier(options).handleEvent(createdCritical());

    expect(FakeNotification.instances).toHaveLength(1);
    const notification = FakeNotification.latest();
    expect(notification.title).toBe('Critical: HighErrorRate');
    expect(notification.body).toBe('Error rate above 5% for 10m\nam-eu · core');
    expect(notification.tag).toBe('promview-alert-42');
  });

  it('passes the event fields a host filter matches on', () => {
    // A host shell filters on these; without them its only view of the event is
    // the rendered title, which is not a selector anyone can reason about.
    const { options } = harness();
    createAlertNotifier(options).handleEvent(createdCritical({ severity: 'CRITICAL' }));

    expect(FakeNotification.latest().labels).toEqual({
      // Normalized, so a rule matches what the console displays rather than
      // whatever casing the source used.
      severity: 'critical',
      alertname: 'HighErrorRate',
      source: 'am-eu',
      team: 'core',
      summary: 'Error rate above 5% for 10m',
    });
  });

  it('keeps the body useful when summary and team are empty', () => {
    const { options } = harness();
    createAlertNotifier(options).handleEvent(createdCritical({ summary: '', team: '' }));

    expect(FakeNotification.latest().body).toBe('am-eu');
  });

  it('ignores non-created events and non-critical severities', () => {
    const { options } = harness();
    const notifier = createAlertNotifier(options);

    notifier.handleEvent(createdCritical({ type: 'alert.updated' }));
    notifier.handleEvent(createdCritical({ type: 'alert.resolved' }));
    notifier.handleEvent(removedEvent());
    notifier.handleEvent(createdCritical({ severity: 'warning' }));
    notifier.handleEvent(createdCritical({ severity: 'info' }));

    expect(FakeNotification.instances).toHaveLength(0);
  });

  it('never notifies for redacted alert.removed events', () => {
    const { options } = harness();
    const notifier = createAlertNotifier(options);

    // Opted in and hidden: every other criterion is met, but a redacted
    // removal carries no context to show and must stay silent.
    notifier.handleEvent(removedEvent());
    notifier.handleEvent(removedEvent({ id: 9 }));

    expect(FakeNotification.instances).toHaveLength(0);
  });

  it('treats severity case-insensitively', () => {
    const { options } = harness();
    createAlertNotifier(options).handleEvent(createdCritical({ severity: 'Critical' }));

    expect(FakeNotification.instances).toHaveLength(1);
  });

  it('suppresses while the document is visible but still records the event', () => {
    const storage = memoryStorage();
    const first = harness(storage);
    first.setHidden(false);
    createAlertNotifier(first.options).handleEvent(createdCritical());
    expect(FakeNotification.instances).toHaveLength(0);

    // A replay after the tab is hidden (e.g. stream resumed) never notifies.
    const second = harness(storage);
    createAlertNotifier(second.options).handleEvent(createdCritical());
    expect(FakeNotification.instances).toHaveLength(0);
  });

  it('suppresses while opted out but still records the event', () => {
    const storage = memoryStorage();
    const first = harness(storage);
    first.setEnabled(false);
    createAlertNotifier(first.options).handleEvent(createdCritical());
    expect(FakeNotification.instances).toHaveLength(0);

    // Opting in later must not surface the old event on replay.
    const second = harness(storage);
    createAlertNotifier(second.options).handleEvent(createdCritical());
    expect(FakeNotification.instances).toHaveLength(0);
  });

  it('dedupes repeated deliveries of the same event id', () => {
    const { options } = harness();
    const notifier = createAlertNotifier(options);

    notifier.handleEvent(createdCritical());
    notifier.handleEvent(createdCritical());
    notifier.handleEvent(createdCritical({ id: 9 }));

    // The repeat id 8 was deduped; the distinct id 9 still notified.
    expect(FakeNotification.instances).toHaveLength(2);
  });

  it('focuses the window and navigates to the alert detail on click', () => {
    const { options, navigateToAlert, focusWindow } = harness();
    createAlertNotifier(options).handleEvent(createdCritical());

    const notification = FakeNotification.latest();
    notification.click();

    expect(focusWindow).toHaveBeenCalledTimes(1);
    expect(navigateToAlert).toHaveBeenCalledWith('42');
    expect(notification.closed).toBe(true);
  });

  it('never lets a throwing constructor break the stream handler', () => {
    const { options } = harness();
    const notifier = createAlertNotifier({
      ...options,
      createNotification: () => {
        throw new Error('not allowed');
      },
    });

    expect(() => notifier.handleEvent(createdCritical())).not.toThrow();
  });
});

describe('eventMatchesSelector', () => {
  const event = createdCritical({ severity: 'critical' });

  it('matches nothing on an empty selector, never everything', () => {
    // Read as vacuous truth, an empty AND would page for every alert in the
    // deployment the moment notifications were switched on.
    expect(eventMatchesSelector(event, [])).toBe(false);
  });

  it('ANDs its matchers', () => {
    expect(
      eventMatchesSelector(event, [
        { name: 'severity', op: '=', value: 'critical' },
        { name: 'source', op: '=', value: event.source },
      ]),
    ).toBe(true);
    expect(
      eventMatchesSelector(event, [
        { name: 'severity', op: '=', value: 'critical' },
        { name: 'source', op: '=', value: 'somewhere-else' },
      ]),
    ).toBe(false);
  });

  it('supports negation, for paging on everything except one noisy source', () => {
    expect(eventMatchesSelector(event, [{ name: 'source', op: '!=', value: 'lab' }])).toBe(true);
    expect(eventMatchesSelector(event, [{ name: 'source', op: '!=', value: event.source }])).toBe(
      false,
    );
  });

  it('normalises severity the way the rest of the console does', () => {
    const shouty = createdCritical({ severity: 'CRITICAL' });
    expect(eventMatchesSelector(shouty, [{ name: 'severity', op: '=', value: 'critical' }])).toBe(
      true,
    );
  });

  it('refuses a field the stream event does not carry', () => {
    // The server rejects such a selector at write time; if one arrives anyway,
    // failing closed beats notifying on everything.
    expect(eventMatchesSelector(event, [{ name: 'instance', op: '=', value: 'web-01' }])).toBe(
      false,
    );
  });
});
