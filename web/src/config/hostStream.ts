import type { EventSourceFactory, EventSourceLike, StreamMessageEvent } from '../alerts/stream';

/**
 * An `EventSource` the host holds open on the console's behalf.
 *
 * The console's reconnect policy is untouched: it still opens, listens, closes
 * and reopens exactly as it does in a browser. All that changes is who holds
 * the socket. That matters because a stream owned by the webview dies with the
 * window, and the tray has to keep counting after the last one closes.
 *
 * The host pushes frames by calling a global this module installs, rather than
 * through Tauri's event plugin, which would put its JS package in a bundle the
 * browser build also ships.
 */

const DISPATCH_GLOBAL = '__PROMVIEW_STREAM__';

type HostMessage =
  | { kind: 'open' }
  | { kind: 'message'; event: string; data: string; id?: string | null }
  | { kind: 'error'; message: string };

type Invoke = (command: string, payload?: unknown) => Promise<unknown>;

interface StreamGlobals {
  [DISPATCH_GLOBAL]?: (message: HostMessage) => void;
}

/**
 * Only one stream is ever live: the console opens a single one and reopens it
 * on reconnect. The host enforces that too, and this keeps a closed source from
 * delivering to listeners that have already been torn down.
 */
let active: HostEventSource | null = null;

class HostEventSource implements EventSourceLike {
  onopen: ((event: Event) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;

  private readonly listeners = new Map<string, Set<(event: StreamMessageEvent) => void>>();
  private lastEventId = '';
  private closed = false;

  constructor(
    private readonly invoke: Invoke,
    path: string,
  ) {
    void this.invoke('stream_start', { path }).catch((cause: unknown) => {
      this.deliverError(cause instanceof Error ? cause.message : String(cause));
    });
  }

  addEventListener(type: string, listener: (event: StreamMessageEvent) => void): void {
    const existing = this.listeners.get(type);
    if (existing === undefined) {
      this.listeners.set(type, new Set([listener]));
      return;
    }
    existing.add(listener);
  }

  removeEventListener(type: string, listener: (event: StreamMessageEvent) => void): void {
    this.listeners.get(type)?.delete(listener);
  }

  close(): void {
    if (this.closed) {
      return;
    }
    this.closed = true;
    if (active === this) {
      active = null;
    }
    void this.invoke('stream_stop').catch(() => {
      // Already gone, or the host is shutting down. Either way this source is
      // closed and will deliver nothing further.
    });
  }

  /** Called by the global the host pushes into. */
  deliver(message: HostMessage): void {
    if (this.closed) {
      return;
    }
    if (message.kind === 'open') {
      this.onopen?.(new Event('open'));
      return;
    }
    if (message.kind === 'error') {
      this.deliverError(message.message);
      return;
    }
    if (typeof message.id === 'string' && message.id !== '') {
      // Held so a resumed stream can pick up where this one stopped, the same
      // way the browser's EventSource tracks it.
      this.lastEventId = message.id;
    }
    const event: StreamMessageEvent = { data: message.data, lastEventId: this.lastEventId };
    for (const listener of this.listeners.get(message.event) ?? []) {
      listener(event);
    }
  }

  private deliverError(message: string): void {
    // The console decides what an error means — retry, back off, give up. This
    // only reports it, in the shape it already handles.
    const event = new Event('error');
    Object.defineProperty(event, 'message', { value: message });
    this.onerror?.(event);
  }
}

/**
 * Installs the dispatch global and returns a factory. Called once when a host
 * is detected; a browser never reaches it.
 */
export function createHostEventSourceFactory(invoke: Invoke, base: string): EventSourceFactory {
  (globalThis as StreamGlobals)[DISPATCH_GLOBAL] = (message: HostMessage) => {
    active?.deliver(message);
  };

  return (url: string) => {
    const path = base !== '' && url.startsWith(base) ? url.slice(base.length) || '/' : url;
    const source = new HostEventSource(invoke, path);
    active = source;
    return source;
  };
}
