import { describe, expect, it } from 'vitest';
import { formatAge, formatTimestamp } from './format';

const now = new Date('2026-08-14T12:00:00Z');

describe('formatAge', () => {
  it('renders compact relative ages', () => {
    expect(formatAge('2026-08-14T11:59:15Z', now)).toBe('45s');
    expect(formatAge('2026-08-14T11:48:00Z', now)).toBe('12m');
    expect(formatAge('2026-08-14T09:00:00Z', now)).toBe('3h');
    expect(formatAge('2026-08-09T12:00:00Z', now)).toBe('5d');
  });

  it('handles unparseable timestamps', () => {
    expect(formatAge('not-a-date', now)).toBe('—');
  });
});

describe('formatTimestamp', () => {
  it('renders full UTC timestamps', () => {
    expect(formatTimestamp('2026-08-14T10:00:00Z')).toBe('2026-08-14 10:00:00 UTC');
    expect(formatTimestamp('2026-08-14T10:00:00.500Z')).toBe('2026-08-14 10:00:00 UTC');
  });

  it('handles unparseable timestamps', () => {
    expect(formatTimestamp('not-a-date')).toBe('—');
  });
});
