import { highestRole } from '../auth/session';
import type { SessionInfo } from '../auth/session';
import type { AuthMode } from '../config/runtimeConfig';
import type { NotificationOptInState } from '../notifications/notifier';
import { PulseMark, UserIcon } from './icons';
import { NotificationOptIn } from './NotificationOptIn';
import { UtcClock } from './UtcClock';

export type ConnectionState = 'loading' | 'ready' | 'error' | 'reconnecting';

const CONNECTION_LABEL: Record<ConnectionState, string> = {
  loading: 'Syncing',
  ready: 'Connected',
  error: 'Offline',
  reconnecting: 'Reconnecting',
};

const MODE_LABEL: Record<AuthMode, string> = {
  open: 'Open access',
  oidc: 'OIDC',
  local: 'Local accounts',
  ldap: 'LDAP',
};

interface TopBarProps {
  productName: string;
  connection: ConnectionState;
  authMode?: AuthMode;
  /** Verified OIDC session; absent while sign-in is pending or unavailable. */
  session?: SessionInfo;
  onSignOut?: () => void;
  signOutPending?: boolean;
  /**
   * Opens the binding administration view. Present only for an administrator:
   * the server refuses everybody else, and a control whose every request
   * answers 403 is worse than no control.
   */
  onOpenAccess?: () => void;
  /** Browser-notification opt-in control; omitted hides it entirely. */
  notificationOptIn?: {
    state: NotificationOptInState;
    onToggle: () => void;
  };
}

/**
 * Compact identity/connection bar: product mark, live connection status,
 * the browser-notification opt-in, deployment auth mode, and the effective
 * identity. In open mode the server grants an anonymous viewer identity,
 * shown here explicitly; in OIDC mode a verified session shows its display
 * name and highest role plus a sign-out action. The connection indicator
 * reflects the live alert stream, not just shell config loading.
 */
export function TopBar({
  productName,
  connection,
  authMode,
  session,
  onSignOut,
  signOutPending = false,
  notificationOptIn,
  onOpenAccess,
}: TopBarProps) {
  const identityName =
    authMode === 'open'
      ? 'Anonymous viewer'
      : session !== undefined
        ? session.displayName
        : 'Sign-in pending';
  const roleBadge = authMode === 'open' ? 'viewer' : session && highestRole(session.roles);

  return (
    <header className="topbar">
      <div className="brand">
        <PulseMark className="brand-mark" />
        <span className="brand-name">{productName}</span>
        <span className="brand-sub">ops console</span>
      </div>
      <div className="topbar-right">
        <UtcClock />
        <span className={`conn conn-${connection}`} role="status">
          <span className="conn-dot" aria-hidden="true" />
          <span className="conn-label">{CONNECTION_LABEL[connection]}</span>
        </span>
        {onOpenAccess !== undefined ? (
          <button type="button" className="badge badge-mode topbar-access" onClick={onOpenAccess}>
            Access
          </button>
        ) : null}
        {notificationOptIn !== undefined ? (
          <NotificationOptIn
            state={notificationOptIn.state}
            onToggle={notificationOptIn.onToggle}
          />
        ) : null}
        {authMode !== undefined ? (
          <span className="badge badge-mode">{MODE_LABEL[authMode]}</span>
        ) : null}
        {authMode !== undefined ? (
          <span className="identity">
            <UserIcon className="identity-icon" />
            <span className="identity-name">{identityName}</span>
            {roleBadge ? <span className="badge badge-role">{roleBadge}</span> : null}
            {session !== undefined && onSignOut !== undefined ? (
              <button
                type="button"
                className="signout-button"
                onClick={onSignOut}
                disabled={signOutPending}
              >
                {signOutPending ? 'Signing out…' : 'Sign out'}
              </button>
            ) : null}
          </span>
        ) : null}
      </div>
    </header>
  );
}
