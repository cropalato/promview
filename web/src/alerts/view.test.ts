import { describe, expect, it } from 'vitest';
import {
  isViewMatch,
  parseViewSummary,
  resolveViewRequest,
  toggledOverride,
  viewKeyOf,
} from './view';

describe('resolveViewRequest', () => {
  it('follows the saved switch when nothing is overridden', () => {
    expect(resolveViewRequest({ enabled: true, criticalBypass: true }, null)).toEqual({
      mode: 'mine',
      criticalBypass: true,
      override: null,
      revision: 0,
    });
    expect(resolveViewRequest({ enabled: false, criticalBypass: true }, null).mode).toBe('all');
  });

  it('lets the moment override the saved switch without changing what is saved', () => {
    const request = resolveViewRequest({ enabled: true, criticalBypass: false }, 'all');
    expect(request).toEqual({ mode: 'all', criticalBypass: false, override: 'all', revision: 0 });
  });
});

describe('toggledOverride', () => {
  it('flips to the other mode', () => {
    expect(toggledOverride({ enabled: true }, 'mine')).toBe('all');
  });

  it('is no override at all when the flip lands on the saved choice', () => {
    // Saved off, currently looking at everything on a whim of the moment: going
    // back to "my view" is a real override...
    expect(toggledOverride({ enabled: false }, 'all')).toBe('mine');
    // ...while leaving an override that merely restores the saved mode clears it.
    expect(toggledOverride({ enabled: true }, 'all')).toBeNull();
    expect(toggledOverride({ enabled: false }, 'mine')).toBeNull();
  });
});

describe('viewKeyOf', () => {
  it('is empty without a view and differs for every change a request can make', () => {
    expect(viewKeyOf(undefined)).toBe('');
    const base = { mode: 'mine' as const, criticalBypass: true, override: null, revision: 0 };
    const keys = new Set([
      viewKeyOf(base),
      viewKeyOf({ ...base, mode: 'all' }),
      viewKeyOf({ ...base, criticalBypass: false }),
      // A join or leave changes what the server derives, with nothing else in the request changing.
      viewKeyOf({ ...base, revision: 1 }),
    ]);
    expect(keys.size).toBe(4);
  });
});

describe('parseViewSummary', () => {
  it('reads what the server reports', () => {
    expect(
      parseViewSummary({ active: true, showingAll: false, hidden: 4, criticalBypass: true }),
    ).toEqual({ active: true, showingAll: false, hidden: 4, criticalBypass: true });
  });

  it('is undefined where there is no view, and for anything malformed', () => {
    expect(parseViewSummary(undefined)).toBeUndefined();
    expect(parseViewSummary(null)).toBeUndefined();
    expect(parseViewSummary([])).toBeUndefined();
    expect(parseViewSummary({ hidden: 3 })).toBeUndefined();
  });

  it('defaults the missing parts safely', () => {
    expect(parseViewSummary({ active: true })).toEqual({
      active: true,
      showingAll: false,
      hidden: 0,
      criticalBypass: true,
    });
  });
});

describe('isViewMatch', () => {
  it('accepts only the three the server sends', () => {
    expect(['view', 'critical', 'outside'].every(isViewMatch)).toBe(true);
    expect(isViewMatch('everything')).toBe(false);
    expect(isViewMatch(undefined)).toBe(false);
  });
});
