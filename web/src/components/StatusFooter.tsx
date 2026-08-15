import type { AlertStreamStatus } from '../alerts/stream';
import type { AuthMode } from '../config/runtimeConfig';

interface StatusFooterProps {
  authMode?: AuthMode;
  /** Live stream state once the first snapshot is up; absent when offline. */
  stream?: AlertStreamStatus;
}

const STREAM_LABEL: Record<AlertStreamStatus, string> = {
  connecting: 'connecting',
  connected: 'live',
  reconnecting: 'reconnecting',
};

/** Slim status line: deployment mode and live-stream state. */
export function StatusFooter({ authMode, stream }: StatusFooterProps) {
  return (
    <footer className="statusbar">
      <span className="statusbar-item">promview console · alertmanager ingestion</span>
      <span className="statusbar-item">
        <span className="statusbar-dot" aria-hidden="true" />
        {authMode === undefined
          ? 'mode: — · stream: offline'
          : `mode: ${authMode} · read-only · stream: ${stream === undefined ? 'offline' : STREAM_LABEL[stream]}`}
      </span>
    </footer>
  );
}
