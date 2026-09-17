import type { OpenModeRole } from '../config/runtimeConfig';

interface OpenModeBannerProps {
  role: OpenModeRole;
  /** The name the server records open-mode actions under. */
  author: string;
}

const ROLE_COPY: Record<Exclude<OpenModeRole, 'viewer'>, string> = {
  operator: 'acknowledge, assign, close, note and silence alerts',
  administrator: 'acknowledge, assign, close, note and silence alerts, and change who can do what',
};

/**
 * Standing notice that this deployment has elevated its open-mode principal.
 *
 * Deliberately not dismissible. A banner that can be closed is a banner that is
 * closed, and what this one reports is not an event that passes: it holds for
 * the life of the deployment, and the operator reading an alert three hours in
 * needs it as much as the one who opened the tab.
 *
 * The author string is named rather than merely alluded to, because it is the
 * whole consequence: every action lands in the audit trail under one mode-shaped
 * name, so nothing done here can be traced back to a person.
 */
export function OpenModeBanner({ role, author }: OpenModeBannerProps) {
  if (role === 'viewer') {
    return null;
  }

  return (
    <div className="notice open-mode-banner" role="status" aria-label="Open access warning">
      <strong className="open-mode-banner-title">
        This console is open to everyone who can reach it
      </strong>
      <p className="open-mode-banner-copy">
        No sign-in is required, and this deployment grants every visitor the <strong>{role}</strong>{' '}
        role: anyone who can open this page can {ROLE_COPY[role]}.
      </p>
      <p className="open-mode-banner-copy">
        Every action is recorded as <code className="open-mode-banner-author">{author}</code> rather
        than as a person, so nothing done here can be traced back to whoever did it.
      </p>
    </div>
  );
}
