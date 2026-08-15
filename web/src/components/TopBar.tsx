import type { AuthMode } from '../config/runtimeConfig';
import { PulseMark, UserIcon } from './icons';
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
  ldap: 'LDAP',
  oidc: 'OIDC',
};

interface TopBarProps {
  productName: string;
  connection: ConnectionState;
  authMode?: AuthMode;
}

/**
 * Compact identity/connection bar: product mark, live connection status,
 * deployment auth mode, and the effective identity. In open mode the server
 * grants an anonymous viewer identity, shown here explicitly. The connection
 * indicator reflects the live alert stream, not just shell config loading.
 */
export function TopBar({ productName, connection, authMode }: TopBarProps) {
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
        {authMode !== undefined ? (
          <span className="badge badge-mode">{MODE_LABEL[authMode]}</span>
        ) : null}
        {authMode !== undefined ? (
          <span className="identity">
            <UserIcon className="identity-icon" />
            <span className="identity-name">
              {authMode === 'open' ? 'Anonymous viewer' : 'Sign-in pending'}
            </span>
            {authMode === 'open' ? <span className="badge badge-role">viewer</span> : null}
          </span>
        ) : null}
      </div>
    </header>
  );
}
