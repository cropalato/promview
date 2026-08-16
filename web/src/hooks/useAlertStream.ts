import { useEffect, useRef, useState } from 'react';
import { createAlertStreamClient } from '../alerts/stream';
import type {
  AlertStreamClient,
  AlertStreamEvent,
  AlertStreamStatus,
  EventSourceFactory,
} from '../alerts/stream';

export interface UseAlertStreamOptions {
  /**
   * Resume cursor from the latest alerts snapshot. The stream connects once
   * the first ready snapshot provides a cursor; later advances are forwarded
   * to the client so a reconnect resumes from the newest position. Passing
   * null withdraws the cursor and closes the stream (session expiry).
   */
  cursor: number | null;
  /** Called for every validated alert lifecycle event. */
  onAlertEvent: (event: AlertStreamEvent) => void;
  /** Transport override for tests and the future Tauri client. */
  factory?: EventSourceFactory;
  retryDelayMs?: number;
}

/**
 * Keeps one alert stream client alive while a snapshot cursor is available.
 * The client is created when the first snapshot cursor arrives, closed
 * (source + reconnect timer) whenever the cursor is withdrawn — the session
 * expired and streaming must stop — and always closed on unmount.
 */
export function useAlertStream({
  cursor,
  onAlertEvent,
  factory,
  retryDelayMs,
}: UseAlertStreamOptions): AlertStreamStatus {
  const [status, setStatus] = useState<AlertStreamStatus>('connecting');
  const clientRef = useRef<AlertStreamClient | null>(null);
  const handlerRef = useRef(onAlertEvent);

  useEffect(() => {
    handlerRef.current = onAlertEvent;
  }, [onAlertEvent]);

  useEffect(() => {
    if (cursor === null) {
      clientRef.current?.close();
      clientRef.current = null;
      setStatus('connecting');
      return;
    }
    if (clientRef.current === null) {
      clientRef.current = createAlertStreamClient({
        cursor,
        factory,
        retryDelayMs,
        onEvent: (event) => {
          handlerRef.current(event);
        },
        onStatus: setStatus,
      });
      return;
    }
    clientRef.current.updateCursor(cursor);
  }, [cursor, factory, retryDelayMs]);

  useEffect(
    () => () => {
      clientRef.current?.close();
      clientRef.current = null;
    },
    [],
  );

  return status;
}
