import { render, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { TopBar } from './TopBar';
import type { AuthMode } from '../config/runtimeConfig';
import type { SessionInfo } from '../auth/session';

/** What an open deployment answers /api/v1/me with: a principal, not a session. */
function openModeSession(overrides: Partial<SessionInfo> = {}): SessionInfo {
  return {
    subject: 'promview-open-mode',
    email: '',
    displayName: 'Anonymous viewer',
    roles: ['viewer'],
    anonymous: true,
    ...overrides,
  };
}

describe('TopBar', () => {
  it('names every mode the deployment can be running', () => {
    // The badge is how an operator tells a locked-down console from an open
    // one at a glance, so a mode with no label of its own is a bug, not a
    // cosmetic gap — which is why MODE_LABEL is keyed by AuthMode itself.
    const labels: Record<AuthMode, string> = {
      open: 'Open access',
      oidc: 'OIDC',
      local: 'Local accounts',
      ldap: 'LDAP',
    };

    for (const [authMode, label] of Object.entries(labels)) {
      const { unmount } = render(
        <TopBar productName="Promview" connection="ready" authMode={authMode as AuthMode} />,
      );
      expect(screen.getByText(label)).toBeInTheDocument();
      unmount();
    }
  });

  it('names the identity and role the server reported, elevated open mode included', () => {
    // Hardcoding "Anonymous viewer" and a viewer badge here was wrong twice
    // over once a deployment could elevate that principal: it named a role
    // nobody held and hid the one they did.
    render(
      <TopBar
        productName="Promview"
        connection="ready"
        authMode="open"
        session={openModeSession({
          displayName: 'Open mode operator',
          roles: ['viewer', 'operator'],
        })}
      />,
    );

    const banner = screen.getByRole('banner');
    expect(within(banner).getByText('Open mode operator')).toBeInTheDocument();
    expect(within(banner).getByText('operator')).toBeInTheDocument();
    expect(within(banner).queryByText('Anonymous viewer')).toBeNull();
  });

  it('still names an unelevated open deployment an anonymous viewer', () => {
    render(
      <TopBar
        productName="Promview"
        connection="ready"
        authMode="open"
        session={openModeSession()}
      />,
    );

    const banner = screen.getByRole('banner');
    expect(within(banner).getByText('Anonymous viewer')).toBeInTheDocument();
    expect(within(banner).getByText('viewer')).toBeInTheDocument();
  });

  it('offers no sign-out for an anonymous principal', () => {
    // There is no session behind it, so the control could only ever revoke
    // something that was never issued — which is why the caller withholds the
    // handler rather than the bar testing anonymity itself.
    render(
      <TopBar
        productName="Promview"
        connection="ready"
        authMode="open"
        session={openModeSession({
          displayName: 'Open mode administrator',
          roles: ['viewer', 'administrator'],
        })}
      />,
    );

    expect(screen.queryByRole('button', { name: /sign out/i })).toBeNull();
  });

  it('offers sign-out where the caller says there is a session to revoke', () => {
    render(
      <TopBar
        productName="Promview"
        connection="ready"
        authMode="oidc"
        session={{
          subject: 'https://idp.example|user-1',
          email: 'ada@example.com',
          displayName: 'Ada Lovelace',
          roles: ['operator'],
          anonymous: false,
        }}
        onSignOut={() => {}}
      />,
    );

    expect(screen.getByRole('button', { name: /sign out/i })).toBeInTheDocument();
  });
});
