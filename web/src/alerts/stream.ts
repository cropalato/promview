/**
 * Live alert event stream transport for `GET /api/v1/stream` (SSE).
 *
 * The stream is resumable: every alert event carries a monotonic id and the
 * server accepts `?cursor=N` (or Last-Event-ID on a browser-initiated
 * reconnect) to replay from that position. This module owns the browser
 * `EventSource` transport behind a small injectable factory so a future
 * Tauri transport can swap in without touching the hooks or components.
 */
export const ALERT_STREAM_URL = '/api/v1/stream';

/** Delay before reconnecting after the stream drops. */
export const STREAM_RETRY_DELAY_MS = 3000;

export const ALERT_STREAM_EVENT_TYPES = [
  'alert.created',
  'alert.updated',
  'alert.resolved',
  'alert.removed',
] as const;

export type AlertStreamEventType = (typeof ALERT_STREAM_EVENT_TYPES)[number];

/** Event types whose payloads carry the denormalized alert context. */
export type AlertStreamNotificationEventType = Exclude<AlertStreamEventType, 'alert.removed'>;

/** Envelope every stream event carries, redacted or not. */
interface AlertStreamEventEnvelope {
  id: number;
  alertId: string;
  occurredAt: string;
}

/**
 * One validated alert lifecycle event that keeps its alert context.
 * Alongside the envelope, the event carries the context the server
 * denormalized into the stream record so clients can react (e.g. browser
 * notifications) without a detail fetch. `summary` and `team` may be empty
 * strings when the source alert lacks them; `severity`, `alertName`, and
 * `source` are always populated (the server falls back for missing labels).
 */
export interface AlertStreamNotificationEvent extends AlertStreamEventEnvelope {
  type: AlertStreamNotificationEventType;
  severity: string;
  alertName: string;
  summary: string;
  source: string;
  team: string;
}

/**
 * A redacted removal event: the alert's context is withheld (the alert is
 * gone and its labels/annotations must not linger in the stream), so only
 * the envelope is available. Clients can refresh list/detail state from
 * `alertId` but must never try to notify from it.
 */
export interface AlertStreamRemovedEvent extends AlertStreamEventEnvelope {
  type: 'alert.removed';
}

/**
 * One validated alert lifecycle event from the stream, discriminated on
 * `type`: created/updated/resolved events expose the full alert context,
 * while `alert.removed` is redacted down to the envelope.
 */
export type AlertStreamEvent = AlertStreamNotificationEvent | AlertStreamRemovedEvent;

/**
 * Live connection state: `connecting` until the first open, `connected`
 * while events flow, `reconnecting` after a drop while a resume attempt is
 * pending or in flight.
 */
export type AlertStreamStatus = 'connecting' | 'connected' | 'reconnecting';

/** Minimal message shape the client consumes; `MessageEvent` satisfies it. */
export interface StreamMessageEvent {
  readonly data: string;
  readonly lastEventId: string;
}

/**
 * Structural subset of the browser `EventSource` the client relies on.
 * Keeping it minimal lets tests (and later the Tauri transport) inject a
 * compatible implementation; the browser `EventSource` satisfies it as-is.
 */
export interface EventSourceLike {
  onopen: ((event: Event) => void) | null;
  onerror: ((event: Event) => void) | null;
  addEventListener(type: string, listener: (event: StreamMessageEvent) => void): void;
  removeEventListener(type: string, listener: (event: StreamMessageEvent) => void): void;
  close(): void;
}

export type EventSourceFactory = (url: string) => EventSourceLike;

/**
 * Default browser transport. Referenced lazily so importing this module
 * stays safe where `EventSource` is undefined (tests, non-browser shells).
 */
export const browserEventSourceFactory: EventSourceFactory = (url) => new EventSource(url);

export function buildAlertStreamUrl(cursor: number): string {
  return `${ALERT_STREAM_URL}?cursor=${String(cursor)}`;
}

/**
 * Validates one SSE payload. Returns null for anything malformed so stray
 * comments/keepalives or schema drift never crash the stream; unknown or
 * mismatched event types are dropped the same way. Redacted `alert.removed`
 * payloads validate against the envelope alone — context fields are neither
 * required nor passed through — while every other type must carry the full
 * denormalized alert context.
 */
export function parseAlertStreamEvent(
  data: string,
  expectedType?: AlertStreamEventType,
): AlertStreamEvent | null {
  let body: unknown;
  try {
    body = JSON.parse(data);
  } catch {
    return null;
  }
  if (typeof body !== 'object' || body === null || Array.isArray(body)) {
    return null;
  }
  const record = body as Record<string, unknown>;
  const type = record.type;
  if (
    typeof type !== 'string' ||
    !ALERT_STREAM_EVENT_TYPES.includes(type as AlertStreamEventType) ||
    (expectedType !== undefined && type !== expectedType)
  ) {
    return null;
  }
  // Cast once past the membership check so the removed/notification split
  // below narrows on the literal union.
  const eventType = type as AlertStreamEventType;
  if (
    typeof record.id !== 'number' ||
    !Number.isFinite(record.id) ||
    typeof record.alertId !== 'string' ||
    record.alertId === '' ||
    typeof record.occurredAt !== 'string' ||
    record.occurredAt === ''
  ) {
    return null;
  }
  const envelope = {
    id: record.id,
    alertId: record.alertId,
    occurredAt: record.occurredAt,
  };
  // Redacted removals carry only the envelope; any context fields a peer
  // might sneak into the payload are dropped, never exposed to clients.
  if (eventType === 'alert.removed') {
    return { ...envelope, type: eventType };
  }
  // Alert context fields are required: severity, alertName, and source must
  // be non-empty (the server backfills them), while summary and team are
  // valid as empty strings when the alert omits them.
  const { severity, alertName, summary, source, team } = record;
  if (
    typeof severity !== 'string' ||
    severity === '' ||
    typeof alertName !== 'string' ||
    alertName === '' ||
    typeof summary !== 'string' ||
    typeof source !== 'string' ||
    source === '' ||
    typeof team !== 'string'
  ) {
    return null;
  }
  return {
    ...envelope,
    type: eventType,
    severity,
    alertName,
    summary,
    source,
    team,
  };
}

export interface AlertStreamClientOptions {
  /** Resume position, from the latest alerts snapshot (`streamCursor`). */
  cursor: number;
  onEvent: (event: AlertStreamEvent) => void;
  onStatus: (status: AlertStreamStatus) => void;
  factory?: EventSourceFactory;
  retryDelayMs?: number;
}

export interface AlertStreamClient {
  /**
   * Advances the resume position when a newer snapshot cursor arrives. A
   * pending reconnect picks it up; a live connection is left alone because
   * replayed events would only re-trigger a refresh.
   */
  updateCursor: (cursor: number) => void;
  /** Closes the source and cancels any pending reconnect. */
  close: () => void;
}

/**
 * Maintains one stream connection at a time, resuming from the latest known
 * cursor after drops. Reconnects are managed explicitly (rather than relying
 * on the browser's automatic retry) so the status callbacks stay accurate
 * and the resume cursor always reflects snapshot progress.
 */
export function createAlertStreamClient(options: AlertStreamClientOptions): AlertStreamClient {
  const factory = options.factory ?? browserEventSourceFactory;
  const retryDelayMs = options.retryDelayMs ?? STREAM_RETRY_DELAY_MS;
  let cursor = options.cursor;
  let source: EventSourceLike | null = null;
  let retryTimer: ReturnType<typeof setTimeout> | null = null;
  let attempts = 0;
  let closed = false;

  const emitStatus = (status: AlertStreamStatus): void => {
    if (!closed) {
      options.onStatus(status);
    }
  };

  const clearRetryTimer = (): void => {
    if (retryTimer !== null) {
      clearTimeout(retryTimer);
      retryTimer = null;
    }
  };

  const handleEvent = (expectedType: AlertStreamEventType, message: StreamMessageEvent): void => {
    if (closed) {
      return;
    }
    const event = parseAlertStreamEvent(message.data, expectedType);
    if (event === null) {
      return;
    }
    // The SSE `id:` field is the authoritative resume position; fall back to
    // the payload id when a transport does not surface Last-Event-ID.
    const messageId = Number(message.lastEventId);
    const resumeFrom = Number.isFinite(messageId) && messageId > 0 ? messageId : event.id;
    if (resumeFrom > cursor) {
      cursor = resumeFrom;
    }
    options.onEvent(event);
  };

  const connect = (): void => {
    if (closed) {
      return;
    }
    clearRetryTimer();
    emitStatus(attempts === 0 ? 'connecting' : 'reconnecting');
    attempts += 1;

    const next = factory(buildAlertStreamUrl(cursor));
    source = next;
    next.onopen = () => {
      if (!closed && source === next) {
        emitStatus('connected');
      }
    };
    next.onerror = () => {
      if (closed || source !== next) {
        return;
      }
      next.close();
      source = null;
      emitStatus('reconnecting');
      retryTimer = setTimeout(connect, retryDelayMs);
    };
    for (const type of ALERT_STREAM_EVENT_TYPES) {
      next.addEventListener(type, (message) => {
        handleEvent(type, message);
      });
    }
  };

  connect();

  return {
    updateCursor(next: number) {
      if (next > cursor) {
        cursor = next;
      }
    },
    close() {
      if (closed) {
        return;
      }
      closed = true;
      clearRetryTimer();
      source?.close();
      source = null;
    },
  };
}
