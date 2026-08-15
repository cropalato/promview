import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { AlertStreamEvent } from '../alerts/stream';
import { FakeEventSource } from '../test/fakeEventSource';
import { useAlertStream } from './useAlertStream';

function streamEventPayload(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: 8,
    type: 'alert.updated',
    alertId: '42',
    occurredAt: '2026-08-14T12:00:00Z',
    ...overrides,
  };
}

function renderStream(
  initialCursor: number | null,
  onAlertEvent: (event: AlertStreamEvent) => void,
) {
  return renderHook(
    ({ cursor }: { cursor: number | null }) =>
      useAlertStream({
        cursor,
        onAlertEvent,
        factory: (url) => new FakeEventSource(url),
        retryDelayMs: 1000,
      }),
    { initialProps: { cursor: initialCursor } },
  );
}

beforeEach(() => {
  FakeEventSource.reset();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('useAlertStream', () => {
  it('waits for the first snapshot cursor, then connects with it in the URL', () => {
    const { result, rerender } = renderStream(null, () => {});
    expect(FakeEventSource.instances).toHaveLength(0);
    expect(result.current).toBe('connecting');

    rerender({ cursor: 7 });
    expect(FakeEventSource.instances).toHaveLength(1);
    expect(FakeEventSource.latest().url).toBe('/api/v1/stream?cursor=7');
    expect(result.current).toBe('connecting');

    act(() => FakeEventSource.latest().emitOpen());
    expect(result.current).toBe('connected');
  });

  it('does not reconnect when the snapshot cursor advances on a healthy stream', () => {
    const { result, rerender } = renderStream(7, () => {});
    act(() => FakeEventSource.latest().emitOpen());
    expect(result.current).toBe('connected');

    rerender({ cursor: 9 });
    expect(FakeEventSource.instances).toHaveLength(1);
    expect(result.current).toBe('connected');
  });

  it('forwards alert events to the handler', () => {
    const events: AlertStreamEvent[] = [];
    renderStream(7, (event) => events.push(event));

    act(() => {
      FakeEventSource.latest().emit('alert.updated', streamEventPayload(), '8');
    });

    expect(events).toEqual([
      { id: 8, type: 'alert.updated', alertId: '42', occurredAt: '2026-08-14T12:00:00Z' },
    ]);
  });

  it('reports reconnecting after a drop and resumes from the newest snapshot cursor', () => {
    vi.useFakeTimers();
    const { result, rerender } = renderStream(7, () => {});
    act(() => FakeEventSource.latest().emitOpen());
    expect(result.current).toBe('connected');

    act(() => FakeEventSource.latest().emitError());
    expect(result.current).toBe('reconnecting');
    expect(FakeEventSource.instances).toHaveLength(1);

    // The snapshot advanced while the stream was down; the resume uses it.
    rerender({ cursor: 12 });
    act(() => {
      vi.advanceTimersByTime(1000);
    });
    expect(FakeEventSource.instances).toHaveLength(2);
    expect(FakeEventSource.latest().url).toBe('/api/v1/stream?cursor=12');

    act(() => FakeEventSource.latest().emitOpen());
    expect(result.current).toBe('connected');
  });

  it('closes the source and reconnect timer on unmount', () => {
    vi.useFakeTimers();
    const { unmount } = renderStream(7, () => {});
    const first = FakeEventSource.latest();
    act(() => first.emitError());

    unmount();
    expect(first.closed).toBe(true);

    act(() => {
      vi.advanceTimersByTime(10_000);
    });
    expect(FakeEventSource.instances).toHaveLength(1);
  });
});
