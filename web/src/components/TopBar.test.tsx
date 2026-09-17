import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { TopBar } from './TopBar';
import type { AuthMode } from '../config/runtimeConfig';

describe('TopBar', () => {
  it('names every mode the deployment can be running', () => {
    // The badge is how an operator tells a locked-down console from an open
    // one at a glance, so a mode with no label of its own is a bug, not a
    // cosmetic gap — which is why MODE_LABEL is keyed by AuthMode itself.
    const labels: Record<AuthMode, string> = {
      open: 'Open access',
      oidc: 'OIDC',
      local: 'Local accounts',
    };

    for (const [authMode, label] of Object.entries(labels)) {
      const { unmount } = render(
        <TopBar productName="Promview" connection="ready" authMode={authMode as AuthMode} />,
      );
      expect(screen.getByText(label)).toBeInTheDocument();
      unmount();
    }
  });
});
