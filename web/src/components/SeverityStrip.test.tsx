import { render, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { SeverityStrip } from './SeverityStrip';

describe('SeverityStrip', () => {
  it('renders zero counts for an empty result', () => {
    render(<SeverityStrip counts={{}} total={0} />);

    expect(screen.getAllByRole('listitem')).toHaveLength(3);
    expect(screen.getByText('Critical')).toBeInTheDocument();
    expect(screen.getByText('Warning')).toBeInTheDocument();
    expect(screen.getByText('Info')).toBeInTheDocument();
    expect(screen.getAllByText('0')).toHaveLength(3);
    expect(screen.getByText('0 firing')).toBeInTheDocument();
  });

  it('uses the server counts, which cover more than the loaded rows', () => {
    render(<SeverityStrip counts={{ critical: 3, warning: 1 }} total={4} />);

    const [critical, warning, info] = screen.getAllByRole('listitem');
    expect(within(critical!).getByText('3')).toBeInTheDocument();
    expect(within(warning!).getByText('1')).toBeInTheDocument();
    expect(within(info!).getByText('0')).toBeInTheDocument();
    expect(screen.getByText('4 firing')).toBeInTheDocument();
  });

  it('folds unknown severities into info like row rendering does', () => {
    render(<SeverityStrip counts={{ page: 2, info: 1, CRITICAL: 1 }} total={4} />);

    const [critical, warning, info] = screen.getAllByRole('listitem');
    expect(within(critical!).getByText('1')).toBeInTheDocument();
    expect(within(warning!).getByText('0')).toBeInTheDocument();
    expect(within(info!).getByText('3')).toBeInTheDocument();
    expect(screen.getByText('4 firing')).toBeInTheDocument();
  });
});
