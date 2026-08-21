import { useId } from 'react';
import type { AlertStreamStatus } from '../alerts/stream';
import type { AuthMode } from '../config/runtimeConfig';
import { THEMES } from '../preferences/theme';
import type { Theme } from '../preferences/theme';

interface StatusFooterProps {
  authMode?: AuthMode;
  /** Live stream state once the first snapshot is up; absent when offline. */
  stream?: AlertStreamStatus;
  theme: Theme;
  onThemeChange: (theme: Theme) => void;
}

const STREAM_LABEL: Record<AlertStreamStatus, string> = {
  connecting: 'connecting',
  connected: 'live',
  reconnecting: 'reconnecting',
};

/**
 * Slim status line: deployment mode, live-stream state and the palette picker.
 *
 * Deliberately says nothing about what the operator may do. The top bar already
 * renders their highest role from the session, and a second, hardcoded claim
 * here contradicted it for anyone above viewer.
 */
export function StatusFooter({ authMode, stream, theme, onThemeChange }: StatusFooterProps) {
  const themeLabelId = useId();
  return (
    <footer className="statusbar">
      <span className="statusbar-item">promview console · alertmanager ingestion</span>
      <span className="statusbar-item">
        <span className="statusbar-dot" aria-hidden="true" />
        {authMode === undefined
          ? 'mode: — · stream: offline'
          : `mode: ${authMode} · stream: ${stream === undefined ? 'offline' : STREAM_LABEL[stream]}`}
      </span>
      <span className="statusbar-item">
        {/* A native select: the footer is one line tall, and keyboard and
            screen-reader behaviour come free rather than being rebuilt. */}
        <label className="visually-hidden" htmlFor={themeLabelId}>
          Theme
        </label>
        <span aria-hidden="true">theme:</span>
        <select
          id={themeLabelId}
          className="theme-select"
          value={theme}
          onChange={(event) => onThemeChange(event.target.value as Theme)}
        >
          {THEMES.map((option) => (
            <option key={option.id} value={option.id}>
              {option.label}
            </option>
          ))}
        </select>
      </span>
    </footer>
  );
}
