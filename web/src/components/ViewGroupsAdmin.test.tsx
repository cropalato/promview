import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ViewGroupsAdmin } from './ViewGroupsAdmin';
import * as api from '../access/viewGroups';
import type { ViewGroup } from '../access/viewGroups';

const platform: ViewGroup = {
  name: 'platform',
  description: 'Platform team',
  joined: false,
  pinned: false,
  matchers: [{ name: 'team', operator: '=', value: 'platform' }],
  pins: [{ subjectKind: 'oidc_group', subjectIssuer: 'https://idp.example', subjectGroup: 'sre' }],
  members: 2,
};

afterEach(() => {
  vi.restoreAllMocks();
});

describe('ViewGroupsAdmin', () => {
  it('shows each group with its selector, members and pins', async () => {
    vi.spyOn(api, 'fetchViewGroups').mockResolvedValue([platform]);
    render(<ViewGroupsAdmin />);

    await waitFor(() => expect(screen.getByText('platform')).toBeTruthy());
    expect(screen.getByText('team=platform')).toBeTruthy();
    expect(screen.getByText('2')).toBeTruthy();
    expect(screen.getByText('sre @ https://idp.example')).toBeTruthy();
  });

  it('says plainly that a group is not a permission', async () => {
    vi.spyOn(api, 'fetchViewGroups').mockResolvedValue([]);
    render(<ViewGroupsAdmin />);
    await waitFor(() => expect(screen.getByText('No view groups yet.')).toBeTruthy());
    expect(screen.getByText(/never grants access/i)).toBeTruthy();
  });

  it('saves a group with its parsed selector', async () => {
    vi.spyOn(api, 'fetchViewGroups').mockResolvedValue([]);
    const save = vi.spyOn(api, 'saveViewGroup').mockResolvedValue();
    render(<ViewGroupsAdmin />);
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Add a view group' })).toBeTruthy(),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Add a view group' }));

    fireEvent.change(screen.getByLabelText('Group name'), { target: { value: 'platform' } });
    fireEvent.change(screen.getByLabelText('Selector'), { target: { value: 'team=platform' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save view group' }));

    await waitFor(() =>
      expect(save).toHaveBeenCalledWith({
        name: 'platform',
        description: '',
        matchers: [{ name: 'team', operator: '=', value: 'platform' }],
      }),
    );
  });

  it('refuses a group with no selector before asking the server', async () => {
    vi.spyOn(api, 'fetchViewGroups').mockResolvedValue([]);
    const save = vi.spyOn(api, 'saveViewGroup').mockResolvedValue();
    render(<ViewGroupsAdmin />);
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Add a view group' })).toBeTruthy(),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Add a view group' }));
    fireEvent.change(screen.getByLabelText('Group name'), { target: { value: 'all' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save view group' }));

    expect(screen.getByRole('alert').textContent).toMatch(/selector/i);
    expect(save).not.toHaveBeenCalled();
  });

  it('pins a group on a directory group', async () => {
    vi.spyOn(api, 'fetchViewGroups').mockResolvedValue([platform]);
    const pin = vi.spyOn(api, 'pinViewGroup').mockResolvedValue();
    render(<ViewGroupsAdmin />);
    await waitFor(() => expect(screen.getByText('platform')).toBeTruthy());
    fireEvent.click(screen.getByRole('button', { name: 'Pin' }));

    const form = screen.getByRole('form', { name: 'Pin platform' });
    fireEvent.change(within(form).getByLabelText('Issuer'), {
      target: { value: 'https://idp.example' },
    });
    fireEvent.change(within(form).getByLabelText('Directory group'), { target: { value: 'dba' } });
    fireEvent.click(within(form).getByRole('button', { name: 'Pin platform' }));

    await waitFor(() =>
      expect(pin).toHaveBeenCalledWith('platform', {
        subjectKind: 'oidc_group',
        subjectIssuer: 'https://idp.example',
        subjectGroup: 'dba',
      }),
    );
  });

  it('unpins and removes, reporting what the server said when it refuses', async () => {
    vi.spyOn(api, 'fetchViewGroups').mockResolvedValue([platform]);
    const unpin = vi.spyOn(api, 'unpinViewGroup').mockResolvedValue();
    vi.spyOn(api, 'deleteViewGroup').mockRejectedValue(new Error('view group not found'));
    render(<ViewGroupsAdmin />);
    await waitFor(() => expect(screen.getByText('platform')).toBeTruthy());

    fireEvent.click(screen.getByRole('button', { name: /Unpin sre @ https:\/\/idp.example/ }));
    await waitFor(() => expect(unpin).toHaveBeenCalledWith('platform', platform.pins[0]));

    fireEvent.click(screen.getByRole('button', { name: 'Remove' }));
    await waitFor(() => expect(screen.getByRole('alert').textContent).toBe('view group not found'));
  });
});
