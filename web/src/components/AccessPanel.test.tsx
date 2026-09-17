import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { AccessPanel } from './AccessPanel';
import * as bindings from '../access/bindings';
import type { RoleBinding } from '../access/bindings';

const scoped: RoleBinding = {
  name: 'platform',
  subjectKind: 'oidc_group',
  subjectIssuer: 'https://idp.example',
  subjectGroup: 'platform',
  role: 'operator',
  matchers: [{ name: 'team', operator: '=', value: 'platform' }],
};

const unscoped: RoleBinding = {
  name: 'admins',
  subjectKind: 'oidc_group',
  subjectIssuer: 'https://idp.example',
  subjectGroup: 'admins',
  role: 'administrator',
  matchers: [],
};

afterEach(() => {
  vi.restoreAllMocks();
});

describe('AccessPanel', () => {
  it('shows each binding with its scope, not just its role', async () => {
    vi.spyOn(bindings, 'fetchRoleBindings').mockResolvedValue([scoped, unscoped]);
    render(<AccessPanel onClose={() => {}} />);

    await waitFor(() => expect(screen.getByText('platform')).toBeTruthy());
    // A viewer bound with team=platform sees a different deployment than one
    // bound without it, so the scope is on the row.
    expect(screen.getByText('team=platform')).toBeTruthy();
    // And an unscoped binding says so rather than showing an empty cell.
    expect(screen.getByText('everything')).toBeTruthy();
  });

  it('explains an empty list rather than showing a bare table', async () => {
    vi.spyOn(bindings, 'fetchRoleBindings').mockResolvedValue([]);
    render(<AccessPanel onClose={() => {}} />);
    await waitFor(() => expect(screen.getByText(/No bindings yet/)).toBeTruthy());
    expect(screen.getByText(/every signed-in identity is denied/)).toBeTruthy();
  });

  it('surfaces the refusal to remove the last administrator', async () => {
    vi.spyOn(bindings, 'fetchRoleBindings').mockResolvedValue([unscoped]);
    vi.spyOn(bindings, 'deleteRoleBinding').mockRejectedValue(
      new Error('refusing to remove the last administrator binding'),
    );
    render(<AccessPanel onClose={() => {}} />);

    await waitFor(() => expect(screen.getByText('admins')).toBeTruthy());
    fireEvent.click(screen.getByRole('button', { name: 'Remove' }));
    // The rule is guidance: an administrator who saw only a status code would
    // have no idea it existed.
    await waitFor(() =>
      expect(screen.getByRole('alert').textContent).toMatch(/last administrator/),
    );
  });

  it('refuses a malformed scope before sending it', async () => {
    vi.spyOn(bindings, 'fetchRoleBindings').mockResolvedValue([]);
    const save = vi.spyOn(bindings, 'saveRoleBinding').mockResolvedValue(undefined);
    render(<AccessPanel onClose={() => {}} />);

    await waitFor(() => expect(screen.getByText(/No bindings yet/)).toBeTruthy());
    fireEvent.click(screen.getByRole('button', { name: 'Add a binding' }));
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'new-binding' } });
    fireEvent.change(screen.getByLabelText('Scope'), { target: { value: 'team' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save binding' }));

    await waitFor(() => expect(screen.getByRole('alert')).toBeTruthy());
    expect(save).not.toHaveBeenCalled();
  });

  it('keeps a regex scope through an edit', async () => {
    const regex: RoleBinding = {
      ...scoped,
      matchers: [{ name: 'host', operator: '=~', value: '^web' }],
    };
    vi.spyOn(bindings, 'fetchRoleBindings').mockResolvedValue([regex]);
    const save = vi.spyOn(bindings, 'saveRoleBinding').mockResolvedValue(undefined);
    render(<AccessPanel onClose={() => {}} />);

    await waitFor(() => expect(screen.getByText('host=~^web')).toBeTruthy());
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    fireEvent.click(screen.getByRole('button', { name: 'Save binding' }));

    // A form that understood four operators as two would quietly rewrite this
    // into host=^web, which is a different and narrower binding.
    await waitFor(() => expect(save).toHaveBeenCalled());
    const sent = save.mock.calls[0]?.[0] as RoleBinding;
    expect(sent.matchers).toEqual([{ name: 'host', operator: '=~', value: '^web' }]);
  });

  it('will not rename an existing binding, because that is a different one', async () => {
    vi.spyOn(bindings, 'fetchRoleBindings').mockResolvedValue([scoped]);
    render(<AccessPanel onClose={() => {}} />);
    await waitFor(() => expect(screen.getByText('platform')).toBeTruthy());
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    expect((screen.getByLabelText('Name') as HTMLInputElement).disabled).toBe(true);
  });
});
