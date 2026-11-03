import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { ViewSummary } from '../alerts/view';
import { ViewBanner } from './ViewBanner';

const active: ViewSummary = { active: true, showingAll: false, hidden: 7, criticalBypass: true };

describe('ViewBanner', () => {
  it('says what the view is hiding and offers the way out', () => {
    const onToggle = vi.fn();
    render(<ViewBanner summary={active} mode="mine" onToggle={onToggle} />);

    expect(screen.getByRole('status')).toHaveTextContent('Showing your view');
    expect(screen.getByText('7 hidden by your view')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Show everything in my scope' }));
    expect(onToggle).toHaveBeenCalledTimes(1);
  });

  it('says plainly when nothing is hidden', () => {
    render(<ViewBanner summary={{ ...active, hidden: 0 }} mode="mine" onToggle={() => {}} />);
    expect(screen.getByText('Nothing is hidden')).toBeInTheDocument();
  });

  it('says what the view would hide while everything is showing, and offers the way back', () => {
    const summary = { ...active, showingAll: true };
    render(<ViewBanner summary={summary} mode="all" onToggle={() => {}} />);

    expect(screen.getByRole('status')).toHaveTextContent('Showing everything in your scope');
    expect(screen.getByText('Your view would hide 7')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Back to my view' })).toBeInTheDocument();
  });

  it('announces the mode and not the count, which moves with every live refresh', () => {
    render(<ViewBanner summary={active} mode="mine" onToggle={() => {}} />);
    expect(screen.getByRole('status')).not.toHaveTextContent('hidden');
  });

  it('renders nothing where there is no view to speak of', () => {
    const { container, rerender } = render(
      <ViewBanner summary={undefined} mode="mine" onToggle={() => {}} />,
    );
    expect(container).toBeEmptyDOMElement();
    rerender(<ViewBanner summary={{ ...active, active: false }} mode="mine" onToggle={() => {}} />);
    expect(container).toBeEmptyDOMElement();
  });
});
