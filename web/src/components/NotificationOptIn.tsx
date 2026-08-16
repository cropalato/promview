import type { JSX } from 'react';
import type { NotificationOptInState } from '../notifications/notifier';
import { BellIcon, BellOffIcon } from './icons';

const STATE_LABEL: Record<NotificationOptInState, string> = {
  enabled: 'Mute critical alert notifications',
  disabled: 'Enable critical alert notifications',
  denied: 'Alert notifications are blocked in the browser settings',
  unsupported: 'This browser does not support notifications',
};

interface NotificationOptInProps {
  state: NotificationOptInState;
  onToggle: () => void;
}

/**
 * Compact top-bar toggle for browser notifications of new critical alerts.
 * The control only reflects state and forwards clicks — the permission
 * prompt, preference persistence, and dispatch logic live in
 * `useAlertNotifications`. Denied and unsupported states are disabled:
 * neither can produce a prompt, so the button becomes a status hint.
 */
export function NotificationOptIn({ state, onToggle }: NotificationOptInProps): JSX.Element {
  const inert = state === 'denied' || state === 'unsupported';
  return (
    <button
      type="button"
      className={`notif-toggle notif-${state}`}
      aria-label={STATE_LABEL[state]}
      aria-pressed={state === 'enabled'}
      title={STATE_LABEL[state]}
      onClick={onToggle}
      disabled={inert}
    >
      {state === 'enabled' ? (
        <BellIcon className="notif-icon" />
      ) : (
        <BellOffIcon className="notif-icon" />
      )}
    </button>
  );
}
