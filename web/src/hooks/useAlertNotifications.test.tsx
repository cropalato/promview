import { act, cleanup, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { AlertStreamNotificationEvent, AlertStreamRemovedEvent } from '../alerts/stream';
import { NOTIFICATION_PREFERENCE_KEY } from '../notifications/store';
import { FakeNotification } from '../test/fakeNotification';
import { useAlertNotifications } from './useAlertNotifications';

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

beforeEach(() => {
  FakeNotification.reset();
  window.localStorage.clear();
  vi.stubGlobal('Notification', FakeNotification);
});

afterEach(() => {
  // Unmount before the stubs go away; a hook still mounted when fetch
  // disappears throws against whichever test runs next.
  cleanup();
  vi.unstubAllGlobals();
});

describe('useAlertNotifications', () => {
  it('starts disabled and enables on click when permission is already granted', () => {
    const { result } = renderHook(() => useAlertNotifications({ navigateToAlert: () => {} }));
    expect(result.current.optInState).toBe('disabled');

    act(() => result.current.toggleOptIn());

    expect(result.current.optInState).toBe('enabled');
    // No prompt was needed: permission was already granted.
    expect(FakeNotification.requestCount).toBe(0);
    expect(window.localStorage.getItem(NOTIFICATION_PREFERENCE_KEY)).toBe('true');

    act(() => result.current.toggleOptIn());
    expect(result.current.optInState).toBe('disabled');
    expect(window.localStorage.getItem(NOTIFICATION_PREFERENCE_KEY)).toBe('false');
  });

  it('restores the persisted preference on mount', () => {
    window.localStorage.setItem(NOTIFICATION_PREFERENCE_KEY, 'true');
    const { result } = renderHook(() => useAlertNotifications({ navigateToAlert: () => {} }));

    expect(result.current.optInState).toBe('enabled');
  });

  it('requests permission only on click and enables when granted', async () => {
    FakeNotification.permission = 'default';
    FakeNotification.nextRequestResult = 'granted';
    const { result } = renderHook(() => useAlertNotifications({ navigateToAlert: () => {} }));
    expect(result.current.optInState).toBe('disabled');
    // Mounting must never prompt.
    expect(FakeNotification.requestCount).toBe(0);

    await act(async () => {
      result.current.toggleOptIn();
      await Promise.resolve();
    });

    expect(FakeNotification.requestCount).toBe(1);
    expect(result.current.optInState).toBe('enabled');
    expect(window.localStorage.getItem(NOTIFICATION_PREFERENCE_KEY)).toBe('true');
  });

  it('handles a rejected permission prompt without enabling or crashing', async () => {
    FakeNotification.permission = 'default';
    FakeNotification.nextRequestError = new Error('prompt failed');
    const { result } = renderHook(() => useAlertNotifications({ navigateToAlert: () => {} }));

    await act(async () => {
      result.current.toggleOptIn();
      await Promise.resolve();
    });

    // The rejection is caught: no unhandled rejection fails this run, the
    // preference stays off and unpersisted, and live state is re-read.
    expect(result.current.optInState).toBe('disabled');
    expect(window.localStorage.getItem(NOTIFICATION_PREFERENCE_KEY)).toBeNull();
    expect(FakeNotification.requestCount).toBe(1);

    // A later click can retry the prompt and succeed.
    FakeNotification.nextRequestResult = 'granted';
    await act(async () => {
      result.current.toggleOptIn();
      await Promise.resolve();
    });
    expect(result.current.optInState).toBe('enabled');
    expect(window.localStorage.getItem(NOTIFICATION_PREFERENCE_KEY)).toBe('true');
  });

  it('reports denied when the prompt is refused and does not enable', async () => {
    FakeNotification.permission = 'default';
    FakeNotification.nextRequestResult = 'denied';
    const { result } = renderHook(() => useAlertNotifications({ navigateToAlert: () => {} }));

    await act(async () => {
      result.current.toggleOptIn();
      await Promise.resolve();
    });

    expect(result.current.optInState).toBe('denied');
    expect(window.localStorage.getItem(NOTIFICATION_PREFERENCE_KEY)).toBeNull();

    // Denied clicks are inert: the browser would not show a prompt anyway.
    act(() => result.current.toggleOptIn());
    expect(FakeNotification.requestCount).toBe(1);
    expect(result.current.optInState).toBe('denied');
  });

  it('reports denied when the browser permission is already denied', () => {
    FakeNotification.permission = 'denied';
    const { result } = renderHook(() => useAlertNotifications({ navigateToAlert: () => {} }));

    expect(result.current.optInState).toBe('denied');
    act(() => result.current.toggleOptIn());
    expect(FakeNotification.requestCount).toBe(0);
    expect(result.current.optInState).toBe('denied');
  });

  it('reports unsupported when the Notification API is missing', () => {
    vi.stubGlobal('Notification', undefined);
    const { result } = renderHook(() => useAlertNotifications({ navigateToAlert: () => {} }));

    expect(result.current.optInState).toBe('unsupported');
    act(() => result.current.toggleOptIn());
    expect(result.current.optInState).toBe('unsupported');
  });

  it('notifies for a new critical alert only while enabled and hidden', () => {
    window.localStorage.setItem(NOTIFICATION_PREFERENCE_KEY, 'true');
    let hidden = true;
    const { result } = renderHook(() =>
      useAlertNotifications({
        navigateToAlert: () => {},
        isDocumentHidden: () => hidden,
      }),
    );

    act(() => result.current.handleEvent(createdCritical()));
    expect(FakeNotification.instances).toHaveLength(1);

    // Visible tab: suppressed.
    hidden = false;
    act(() => result.current.handleEvent(createdCritical({ id: 9 })));
    expect(FakeNotification.instances).toHaveLength(1);

    // Not matching criteria: no notification, no ledger churn that matters.
    hidden = true;
    act(() => result.current.handleEvent(createdCritical({ id: 10, severity: 'warning' })));
    act(() => result.current.handleEvent(createdCritical({ id: 11, type: 'alert.updated' })));
    // Redacted removals never notify, even while enabled and hidden.
    act(() => result.current.handleEvent(removedEvent({ id: 12 })));
    expect(FakeNotification.instances).toHaveLength(1);
  });

  it('stays silent while disabled but dedupes the events for later', () => {
    const { result } = renderHook(() =>
      useAlertNotifications({ navigateToAlert: () => {}, isDocumentHidden: () => true }),
    );

    act(() => result.current.handleEvent(createdCritical()));
    expect(FakeNotification.instances).toHaveLength(0);

    // Opt in; a replay of the already-seen event must not notify.
    act(() => result.current.toggleOptIn());
    act(() => result.current.handleEvent(createdCritical()));
    expect(FakeNotification.instances).toHaveLength(0);

    // A genuinely new event notifies immediately after opting in.
    act(() => result.current.handleEvent(createdCritical({ id: 9 })));
    expect(FakeNotification.instances).toHaveLength(1);
  });

  it('routes notification clicks through the navigation seam and focuses the window', () => {
    window.localStorage.setItem(NOTIFICATION_PREFERENCE_KEY, 'true');
    const navigateToAlert = vi.fn();
    const focusWindow = vi.fn();
    const { result } = renderHook(() =>
      useAlertNotifications({
        navigateToAlert,
        focusWindow,
        isDocumentHidden: () => true,
      }),
    );

    act(() => result.current.handleEvent(createdCritical()));
    act(() => FakeNotification.latest().click());

    expect(focusWindow).toHaveBeenCalledTimes(1);
    expect(navigateToAlert).toHaveBeenCalledWith('42');
  });
});
