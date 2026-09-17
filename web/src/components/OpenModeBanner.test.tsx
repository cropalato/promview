import { render, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { OpenModeBanner } from './OpenModeBanner';

describe('OpenModeBanner', () => {
  it('warns when the deployment grants its anonymous principal the operator role', () => {
    render(<OpenModeBanner role="operator" author="lab-console" />);

    const notice = screen.getByRole('status');
    expect(notice).toHaveTextContent(/open to everyone who can reach it/i);
    expect(notice).toHaveTextContent(/operator/);
  });

  it('warns for an administrator too, and says what that adds', () => {
    render(<OpenModeBanner role="administrator" author="lab-console" />);

    const notice = screen.getByRole('status');
    expect(notice).toHaveTextContent(/administrator/);
    expect(notice).toHaveTextContent(/change who can do what/i);
  });

  it('names the author actions are recorded under', () => {
    // The whole consequence of an elevated open mode: the audit trail carries
    // one mode-shaped name, so nothing in it points at a person.
    render(<OpenModeBanner role="operator" author="lab-console" />);

    const notice = screen.getByRole('status');
    expect(notice).toHaveTextContent('lab-console');
    expect(notice).toHaveTextContent(/traced back/i);
  });

  it('renders nothing for an unelevated open deployment', () => {
    // A read-only anonymous viewer is what open mode has always been; a banner
    // there would be noise on every console that never elevated anything.
    const { container } = render(<OpenModeBanner role="viewer" author="promview-open-mode" />);

    expect(container).toBeEmptyDOMElement();
  });

  it('offers no way to dismiss it', () => {
    // A banner that can be closed is a banner that is closed, and what this one
    // reports holds for the life of the deployment rather than passing.
    render(<OpenModeBanner role="administrator" author="lab-console" />);

    expect(screen.queryByRole('button')).toBeNull();
    expect(within(screen.getByRole('status')).queryByRole('button')).toBeNull();
  });
});
