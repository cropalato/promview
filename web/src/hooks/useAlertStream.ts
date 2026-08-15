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
   * to the client so a reconnect resumes from the newest position.
   */
  cursor: number | null;
  /** Called for every validated alert lifecycle event. */
  onAlertEvent: (event: AlertStreamEvent) => void;
  /** Transport override for tests and the future Tauri client. */
  factory?: EventSourceFactory;
  retryDelayMs?: number;
}

/**
 * Keeps one alert stream client alive for the lifetime of the component.
 * The client is created when the first snapshot cursor arrives and is always
 * closed (source + reconnect timer) on unmount.
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
