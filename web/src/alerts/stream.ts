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
] as const;

export type AlertStreamEventType = (typeof ALERT_STREAM_EVENT_TYPES)[number];

/** One validated alert lifecycle event from the stream. */
export interface AlertStreamEvent {
  id: number;
  type: AlertStreamEventType;
  alertId: string;
  occurredAt: string;
}

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
 * mismatched event types are dropped the same way.
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
  return {
    id: record.id,
    type: type as AlertStreamEventType,
    alertId: record.alertId,
    occurredAt: record.occurredAt,
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
