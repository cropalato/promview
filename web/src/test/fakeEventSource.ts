import type { EventSourceLike, StreamMessageEvent } from '../alerts/stream';

/**
 * Test double for the browser `EventSource`. Records every instance so tests
 * can assert connection URLs, drive open/error/event frames manually, and
 * verify cleanup. With `autoOpen` the fake opens on the next microtask,
 * mirroring a healthy browser connect for App-level tests.
 */
export class FakeEventSource implements EventSourceLike {
  static instances: FakeEventSource[] = [];

  readonly url: string;
  onopen: ((event: Event) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  closed = false;
  private readonly listeners = new Map<string, Set<(event: StreamMessageEvent) => void>>();

  constructor(url: string, options: { autoOpen?: boolean } = {}) {
    this.url = url;
    FakeEventSource.instances.push(this);
    if (options.autoOpen === true) {
      queueMicrotask(() => {
        if (!this.closed) {
          this.emitOpen();
        }
      });
    }
  }

  static reset(): void {
    FakeEventSource.instances = [];
  }

  static latest(): FakeEventSource {
    const latest = FakeEventSource.instances[FakeEventSource.instances.length - 1];
    if (latest === undefined) {
      throw new Error('No FakeEventSource has been created yet');
    }
    return latest;
  }

  addEventListener(type: string, listener: (event: StreamMessageEvent) => void): void {
    const listeners = this.listeners.get(type) ?? new Set();
    listeners.add(listener);
    this.listeners.set(type, listeners);
  }

  removeEventListener(type: string, listener: (event: StreamMessageEvent) => void): void {
    this.listeners.get(type)?.delete(listener);
  }

  close(): void {
    this.closed = true;
  }

  emitOpen(): void {
    this.onopen?.(new Event('open'));
  }

  emitError(): void {
    this.onerror?.(new Event('error'));
  }

  /** Delivers one named SSE frame; payloads are JSON-encoded like the server. */
  emit(type: string, payload: unknown, lastEventId = ''): void {
    const message: StreamMessageEvent = {
      data: typeof payload === 'string' ? payload : JSON.stringify(payload),
      lastEventId,
    };
    for (const listener of this.listeners.get(type) ?? []) {
      listener(message);
    }
  }
}

/** Auto-opening variant used when the fake is installed as the global EventSource. */
export class AutoOpenEventSource extends FakeEventSource {
  constructor(url: string) {
    super(url, { autoOpen: true });
  }
}
