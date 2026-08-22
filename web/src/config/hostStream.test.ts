import { afterEach, describe, expect, it, vi } from 'vitest';
import { createHostEventSourceFactory } from './hostStream';
import type { StreamMessageEvent } from '../alerts/stream';

const DISPATCH = '__PROMVIEW_STREAM__';

type HostMessage =
  | { kind: 'open' }
  | { kind: 'message'; event: string; data: string; id?: string | null }
  | { kind: 'error'; message: string };

/** Stands in for the host pushing a frame into the page. */
function push(message: HostMessage): void {
  const dispatch = (globalThis as unknown as Record<string, unknown>)[DISPATCH];
  if (typeof dispatch !== 'function') {
    throw new Error('the host dispatch global was never installed');
  }
  (dispatch as (m: HostMessage) => void)(message);
}

afterEach(() => {
  delete (globalThis as Record<string, unknown>)[DISPATCH];
});

describe('host event source', () => {
  it('asks the host to stream a path, not a URL', () => {
    const invoke = vi.fn().mockResolvedValue(undefined);
    const factory = createHostEventSourceFactory(invoke, 'https://promview.example');
    factory('https://promview.example/api/v1/stream?cursor=7');

    expect(invoke).toHaveBeenCalledWith('stream_start', { path: '/api/v1/stream?cursor=7' });
  });

  it('delivers frames to the listener registered for their event type', () => {
    const invoke = vi.fn().mockResolvedValue(undefined);
    const source = createHostEventSourceFactory(invoke, '')('/api/v1/stream?cursor=0');
    const created: StreamMessageEvent[] = [];
    const updated: StreamMessageEvent[] = [];
    source.addEventListener('alert.created', (event) => created.push(event));
    source.addEventListener('alert.updated', (event) => updated.push(event));

    push({ kind: 'message', event: 'alert.created', data: '{"id":1}', id: '7' });

    expect(created).toEqual([{ data: '{"id":1}', lastEventId: '7' }]);
    expect(updated).toEqual([]);
  });

  it('carries the last id forward so a resume knows where it was', () => {
    const source = createHostEventSourceFactory(vi.fn().mockResolvedValue(undefined), '')('/s');
    const seen: StreamMessageEvent[] = [];
    source.addEventListener('alert.created', (event) => seen.push(event));

    push({ kind: 'message', event: 'alert.created', data: 'a', id: '7' });
    // A frame with no id of its own inherits the last one, as EventSource does.
    push({ kind: 'message', event: 'alert.created', data: 'b' });

    expect(seen.map((event) => event.lastEventId)).toEqual(['7', '7']);
  });

  it('reports open and error through the handlers the console already sets', () => {
    const source = createHostEventSourceFactory(vi.fn().mockResolvedValue(undefined), '')('/s');
    const opens: Event[] = [];
    const errors: Event[] = [];
    source.onopen = (event) => opens.push(event);
    source.onerror = (event) => errors.push(event);

    push({ kind: 'open' });
    push({ kind: 'error', message: 'stream closed by the server' });

    expect(opens).toHaveLength(1);
    expect(errors).toHaveLength(1);
    // The console decides what an error means; this only reports it.
    expect((errors[0] as Event & { message?: string }).message).toBe('stream closed by the server');
  });

  it('stops the host stream on close and delivers nothing afterwards', () => {
    const invoke = vi.fn().mockResolvedValue(undefined);
    const source = createHostEventSourceFactory(invoke, '')('/s');
    const seen: StreamMessageEvent[] = [];
    source.addEventListener('alert.created', (event) => seen.push(event));

    source.close();
    expect(invoke).toHaveBeenCalledWith('stream_stop');

    push({ kind: 'message', event: 'alert.created', data: 'late' });
    expect(seen).toEqual([]);
  });

  it('delivers only to the newest source, since the console keeps one open', () => {
    const invoke = vi.fn().mockResolvedValue(undefined);
    const factory = createHostEventSourceFactory(invoke, '');
    const first = factory('/s?cursor=1');
    const firstSeen: StreamMessageEvent[] = [];
    first.addEventListener('alert.created', (event) => firstSeen.push(event));

    // A reconnect: the console opens a new source. The old one must not also
    // receive, or every event would be handled twice.
    const second = factory('/s?cursor=2');
    const secondSeen: StreamMessageEvent[] = [];
    second.addEventListener('alert.created', (event) => secondSeen.push(event));

    push({ kind: 'message', event: 'alert.created', data: 'x' });

    expect(firstSeen).toEqual([]);
    expect(secondSeen).toHaveLength(1);
  });

  it('surfaces a refused start as an error rather than hanging', async () => {
    const invoke = vi.fn().mockRejectedValue(new Error('would leave the configured server'));
    const source = createHostEventSourceFactory(invoke, '')('/s');
    const errors: Event[] = [];
    source.onerror = (event) => errors.push(event);

    await Promise.resolve();
    await Promise.resolve();

    expect(errors).toHaveLength(1);
  });

  it('tolerates a listener removed mid-stream', () => {
    const source = createHostEventSourceFactory(vi.fn().mockResolvedValue(undefined), '')('/s');
    const seen: StreamMessageEvent[] = [];
    const listener = (event: StreamMessageEvent) => seen.push(event);
    source.addEventListener('alert.created', listener);
    source.removeEventListener('alert.created', listener);

    push({ kind: 'message', event: 'alert.created', data: 'x' });
    expect(seen).toEqual([]);
  });
});
