import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FakeEventSource } from '../test/fakeEventSource';
import { buildAlertStreamUrl, createAlertStreamClient, parseAlertStreamEvent } from './stream';
import type {
  AlertStreamEvent,
  AlertStreamNotificationEvent,
  AlertStreamRemovedEvent,
  AlertStreamStatus,
} from './stream';

function streamEventPayload(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: 8,
    type: 'alert.updated',
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

function streamEvent(
  overrides: Partial<AlertStreamNotificationEvent> = {},
): AlertStreamNotificationEvent {
  return {
    id: 8,
    type: 'alert.updated',
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

/** Redacted payload matching the server schema for removals: envelope only. */
function removedEventPayload(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: 8,
    type: 'alert.removed',
    alertId: '42',
    occurredAt: '2026-08-14T12:00:00Z',
    ...overrides,
  };
}

function removedStreamEvent(
  overrides: Partial<AlertStreamRemovedEvent> = {},
): AlertStreamRemovedEvent {
  return {
    id: 8,
    type: 'alert.removed',
    alertId: '42',
    occurredAt: '2026-08-14T12:00:00Z',
    ...overrides,
  };
}

function collect(): {
  events: AlertStreamEvent[];
  statuses: AlertStreamStatus[];
  onEvent: (event: AlertStreamEvent) => void;
  onStatus: (status: AlertStreamStatus) => void;
} {
  const events: AlertStreamEvent[] = [];
  const statuses: AlertStreamStatus[] = [];
  return {
    events,
    statuses,
    onEvent: (event) => events.push(event),
    onStatus: (status) => statuses.push(status),
  };
}

beforeEach(() => {
  FakeEventSource.reset();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('buildAlertStreamUrl', () => {
  it('targets the stream endpoint with the resume cursor', () => {
    expect(buildAlertStreamUrl(7)).toBe('/api/v1/stream?cursor=7');
    expect(buildAlertStreamUrl(0)).toBe('/api/v1/stream?cursor=0');
  });
});

describe('parseAlertStreamEvent', () => {
  it('parses a well-formed event payload', () => {
    expect(parseAlertStreamEvent(JSON.stringify(streamEventPayload()), 'alert.updated')).toEqual(
      streamEvent(),
    );
  });

  it('rejects malformed payloads and type drift', () => {
    expect(parseAlertStreamEvent('not json', 'alert.updated')).toBeNull();
    expect(parseAlertStreamEvent('null', 'alert.updated')).toBeNull();
    expect(
      parseAlertStreamEvent(JSON.stringify(streamEventPayload({ id: '8' })), 'alert.updated'),
    ).toBeNull();
    expect(
      parseAlertStreamEvent(
        JSON.stringify(streamEventPayload({ type: 'comment' })),
        'alert.updated',
      ),
    ).toBeNull();
    // Channel and payload type must agree.
    expect(parseAlertStreamEvent(JSON.stringify(streamEventPayload()), 'alert.created')).toBeNull();
    expect(
      parseAlertStreamEvent(JSON.stringify(streamEventPayload({ alertId: '' })), 'alert.updated'),
    ).toBeNull();
    expect(
      parseAlertStreamEvent(
        JSON.stringify(streamEventPayload({ occurredAt: undefined })),
        'alert.updated',
      ),
    ).toBeNull();
  });

  it('requires the alert context fields', () => {
    // Missing entirely.
    for (const field of ['severity', 'alertName', 'summary', 'source', 'team']) {
      expect(
        parseAlertStreamEvent(
          JSON.stringify(streamEventPayload({ [field]: undefined })),
          'alert.updated',
        ),
      ).toBeNull();
    }
    // Wrong type.
    expect(
      parseAlertStreamEvent(JSON.stringify(streamEventPayload({ severity: 3 })), 'alert.updated'),
    ).toBeNull();
    // severity, alertName, and source must be non-empty.
    for (const field of ['severity', 'alertName', 'source']) {
      expect(
        parseAlertStreamEvent(JSON.stringify(streamEventPayload({ [field]: '' })), 'alert.updated'),
      ).toBeNull();
    }
  });

  it('accepts empty summary and team when the alert omits them', () => {
    expect(
      parseAlertStreamEvent(
        JSON.stringify(streamEventPayload({ summary: '', team: '' })),
        'alert.updated',
      ),
    ).toEqual(streamEvent({ summary: '', team: '' }));
  });

  it('parses a redacted alert.removed payload from the envelope alone', () => {
    expect(parseAlertStreamEvent(JSON.stringify(removedEventPayload()), 'alert.removed')).toEqual(
      removedStreamEvent(),
    );
  });

  it('never exposes alert context from an alert.removed payload', () => {
    // Even if a peer sneaks context fields into the frame, the parsed event
    // stays redacted down to the envelope.
    expect(
      parseAlertStreamEvent(
        JSON.stringify(
          removedEventPayload({
            severity: 'critical',
            alertName: 'HighErrorRate',
            summary: 'Error rate above 5% for 10m',
            source: 'am-eu',
            team: 'core',
          }),
        ),
        'alert.removed',
      ),
    ).toEqual(removedStreamEvent());
  });

  it('still validates the envelope and channel of alert.removed events', () => {
    // Envelope fields remain required.
    for (const field of ['id', 'alertId', 'occurredAt']) {
      expect(
        parseAlertStreamEvent(
          JSON.stringify(removedEventPayload({ [field]: undefined })),
          'alert.removed',
        ),
      ).toBeNull();
    }
    expect(
      parseAlertStreamEvent(JSON.stringify(removedEventPayload({ alertId: '' })), 'alert.removed'),
    ).toBeNull();
    expect(
      parseAlertStreamEvent(JSON.stringify(removedEventPayload({ id: '8' })), 'alert.removed'),
    ).toBeNull();
    // Channel and payload type must agree.
    expect(
      parseAlertStreamEvent(JSON.stringify(removedEventPayload()), 'alert.updated'),
    ).toBeNull();
    expect(parseAlertStreamEvent(JSON.stringify(streamEventPayload()), 'alert.removed')).toBeNull();
  });
});

describe('createAlertStreamClient', () => {
  it('connects with the snapshot cursor and reports connection states', () => {
    const { statuses, onEvent, onStatus } = collect();
    const client = createAlertStreamClient({
      cursor: 7,
      onEvent,
      onStatus,
      factory: (url) => new FakeEventSource(url),
    });

    expect(FakeEventSource.instances).toHaveLength(1);
    expect(FakeEventSource.latest().url).toBe('/api/v1/stream?cursor=7');
    expect(statuses).toEqual(['connecting']);

    FakeEventSource.latest().emitOpen();
    expect(statuses).toEqual(['connecting', 'connected']);
    client.close();
  });

  it('forwards validated alert events', () => {
    const { events, onEvent, onStatus } = collect();
    const client = createAlertStreamClient({
      cursor: 7,
      onEvent,
      onStatus,
      factory: (url) => new FakeEventSource(url),
    });

    FakeEventSource.latest().emit('alert.updated', streamEventPayload(), '8');

    expect(events).toEqual([streamEvent()]);
    client.close();
  });

  it('forwards redacted alert.removed events', () => {
    const { events, onEvent, onStatus } = collect();
    const client = createAlertStreamClient({
      cursor: 7,
      onEvent,
      onStatus,
      factory: (url) => new FakeEventSource(url),
    });

    FakeEventSource.latest().emit('alert.removed', removedEventPayload(), '8');

    expect(events).toEqual([removedStreamEvent()]);
    client.close();
  });

  it('drops malformed frames without interrupting the stream', () => {
    const { events, onEvent, onStatus } = collect();
    const client = createAlertStreamClient({
      cursor: 7,
      onEvent,
      onStatus,
      factory: (url) => new FakeEventSource(url),
    });
    const source = FakeEventSource.latest();

    source.emit('alert.created', 'not json');
    source.emit('alert.created', streamEventPayload({ id: 'x', type: 'alert.created' }));
    source.emit('alert.created', streamEventPayload({ type: 'alert.created' }), '8');
    source.emit('alert.updated', streamEventPayload({ type: 'alert.created' }));
    // Redacted frames are validated against the envelope too.
    source.emit('alert.removed', removedEventPayload({ occurredAt: '' }));
    source.emit('alert.removed', removedEventPayload({ id: 9 }), '9');

    expect(events).toEqual([streamEvent({ type: 'alert.created' }), removedStreamEvent({ id: 9 })]);
    client.close();
  });

  it('reconnects after a drop, resuming from the last event id', () => {
    vi.useFakeTimers();
    const { events, statuses, onEvent, onStatus } = collect();
    const client = createAlertStreamClient({
      cursor: 7,
      onEvent,
      onStatus,
      factory: (url) => new FakeEventSource(url),
      retryDelayMs: 1000,
    });
    const first = FakeEventSource.latest();
    first.emitOpen();
    first.emit('alert.updated', streamEventPayload(), '8');
    expect(events).toHaveLength(1);

    first.emitError();
    expect(first.closed).toBe(true);
    expect(statuses).toEqual(['connecting', 'connected', 'reconnecting']);
    expect(FakeEventSource.instances).toHaveLength(1);

    vi.advanceTimersByTime(1000);
    expect(FakeEventSource.instances).toHaveLength(2);
    expect(FakeEventSource.latest().url).toBe('/api/v1/stream?cursor=8');
    // A retry attempt reports reconnecting, not a fresh connecting.
    expect(statuses[statuses.length - 1]).toBe('reconnecting');

    FakeEventSource.latest().emitOpen();
    expect(statuses[statuses.length - 1]).toBe('connected');
    client.close();
  });

  it('resumes from an advanced snapshot cursor after a drop', () => {
    vi.useFakeTimers();
    const { onEvent, onStatus } = collect();
    const client = createAlertStreamClient({
      cursor: 5,
      onEvent,
      onStatus,
      factory: (url) => new FakeEventSource(url),
      retryDelayMs: 1000,
    });
    FakeEventSource.latest().emitError();

    // A quiet refresh advanced the snapshot while the stream was down.
    client.updateCursor(12);
    // Older cursors never move the resume position backwards.
    client.updateCursor(9);

    vi.advanceTimersByTime(1000);
    expect(FakeEventSource.latest().url).toBe('/api/v1/stream?cursor=12');
    client.close();
  });

  it('close() closes the source, cancels the reconnect timer, and mutes callbacks', () => {
    vi.useFakeTimers();
    const { events, statuses, onEvent, onStatus } = collect();
    const client = createAlertStreamClient({
      cursor: 3,
      onEvent,
      onStatus,
      factory: (url) => new FakeEventSource(url),
      retryDelayMs: 1000,
    });
    const first = FakeEventSource.latest();
    first.emitError();
    const statusCount = statuses.length;

    client.close();
    expect(first.closed).toBe(true);

    vi.advanceTimersByTime(10_000);
    expect(FakeEventSource.instances).toHaveLength(1);

    first.emitOpen();
    first.emit('alert.created', streamEventPayload({ type: 'alert.created' }), '4');
    expect(statuses).toHaveLength(statusCount);
    expect(events).toEqual([]);
  });
});
