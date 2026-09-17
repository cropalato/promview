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
  /**
   * The principal the server reported, anonymous or not; absent while the
   * identity request is still out or has failed.
   */
  session?: SessionInfo;
  /**
   * Revokes the session. Present only where there is one to revoke: the
   * anonymous principal open mode answers with was never issued, so the control
   * would only ever revoke something that does not exist.
   */
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
 * identity. The name and role badge come from whatever the server reported,
 * anonymous or not: an open-mode deployment can elevate its anonymous
 * principal, and a bar that hardcoded "Anonymous viewer" would then be naming
 * a role nobody here holds. Only the sign-out action turns on there being a
 * session to revoke. The connection indicator reflects the live alert stream,
 * not just shell config loading.
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
  // Placeholders only for the window before the identity request answers. Open
  // mode's is the unelevated reading, because claiming a role the deployment
  // may not grant is the worse of the two ways to be briefly wrong.
  const identityName =
    session !== undefined
      ? session.displayName
      : authMode === 'open'
        ? 'Anonymous viewer'
        : 'Sign-in pending';
  const roleBadge =
    session !== undefined ? highestRole(session.roles) : authMode === 'open' ? 'viewer' : undefined;

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
            {onSignOut !== undefined ? (
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
