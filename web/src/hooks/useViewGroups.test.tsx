import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import * as api from '../access/viewGroups';
import { useViewGroups } from './useViewGroups';

const group = (overrides: Partial<api.ViewGroup> = {}): api.ViewGroup => ({
  name: 'platform',
  description: '',
  joined: false,
  pinned: false,
  matchers: [],
  pins: [],
  members: 0,
  ...overrides,
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('useViewGroups', () => {
  it('loads nothing until it is enabled', () => {
    const fetch = vi.spyOn(api, 'fetchViewGroups').mockResolvedValue([group()]);
    const { result } = renderHook(() => useViewGroups(false, vi.fn()));
    expect(fetch).not.toHaveBeenCalled();
    expect(result.current.groups).toEqual([]);
  });

  it('reports the change only after the server accepted it and the list was reloaded', async () => {
    const order: string[] = [];
    vi.spyOn(api, 'fetchViewGroups')
      .mockResolvedValueOnce([group()])
      .mockImplementationOnce(() => {
        order.push('reload');
        return Promise.resolve([group({ joined: true })]);
      });
    vi.spyOn(api, 'joinViewGroup').mockImplementation(() => {
      order.push('join');
      return Promise.resolve();
    });
    const onChanged = vi.fn(() => order.push('changed'));
    const { result } = renderHook(() => useViewGroups(true, onChanged));
    await waitFor(() => expect(result.current.groups).toHaveLength(1));

    act(() => result.current.join('platform'));

    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1));
    // A refetch made before the membership landed would read the one from before.
    expect(order).toEqual(['join', 'reload', 'changed']);
    expect(result.current.groups[0]?.joined).toBe(true);
  });

  it('keeps the list and does not report a change when leaving is refused', async () => {
    vi.spyOn(api, 'fetchViewGroups').mockResolvedValue([group({ pinned: true })]);
    vi.spyOn(api, 'leaveViewGroup').mockRejectedValue(new Error('view group is pinned'));
    const onChanged = vi.fn();
    const { result } = renderHook(() => useViewGroups(true, onChanged));
    await waitFor(() => expect(result.current.groups).toHaveLength(1));

    act(() => result.current.leave('platform'));

    await waitFor(() => expect(result.current.error).toBe('view group is pinned'));
    expect(onChanged).not.toHaveBeenCalled();
    expect(result.current.pending).toBe(false);
  });
});
