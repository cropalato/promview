import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { StatusFooter } from './StatusFooter';
import { THEMES } from '../preferences/theme';

describe('StatusFooter', () => {
  it('reports the deployment mode and the live stream state', () => {
    render(
      <StatusFooter authMode="oidc" stream="connected" theme="system" onThemeChange={() => {}} />,
    );
    expect(screen.getByText(/mode: oidc/)).toBeInTheDocument();
    expect(screen.getByText(/stream: live/)).toBeInTheDocument();
  });

  it('says the stream is offline before the first snapshot', () => {
    render(<StatusFooter authMode="open" theme="system" onThemeChange={() => {}} />);
    expect(screen.getByText(/stream: offline/)).toBeInTheDocument();
  });

  it('shows the palette in use', () => {
    render(<StatusFooter authMode="open" theme="gruvbox" onThemeChange={() => {}} />);
    expect(screen.getByRole('combobox', { name: /theme/i })).toHaveValue('gruvbox');
  });

  it('forwards a palette change', () => {
    const onThemeChange = vi.fn();
    render(<StatusFooter authMode="open" theme="system" onThemeChange={onThemeChange} />);

    const picker = screen.getByRole('combobox', { name: /theme/i });
    for (const option of THEMES) {
      expect(screen.getByRole('option', { name: option.label })).toBeInTheDocument();
    }

    fireEvent.change(picker, { target: { value: 'colorblind-safe' } });
    expect(onThemeChange).toHaveBeenCalledWith('colorblind-safe');
  });
});
