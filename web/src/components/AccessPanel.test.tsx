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

const ldapGroup: RoleBinding = {
  name: 'directory-admins',
  subjectKind: 'ldap_group',
  subjectIssuer: 'ldaps://directory.example',
  subjectGroup: 'cn=admins,ou=groups,dc=example',
  role: 'administrator',
  matchers: [],
};

/** Opens the add-binding form with a name filled in, ready for the fields under test. */
async function openForm(): Promise<void> {
  await waitFor(() => expect(screen.getByRole('button', { name: 'Add a binding' })).toBeTruthy());
  fireEvent.click(screen.getByRole('button', { name: 'Add a binding' }));
  fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'new-binding' } });
}

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

  it('shows an LDAP group binding as the group at its directory', async () => {
    vi.spyOn(bindings, 'fetchRoleBindings').mockResolvedValue([ldapGroup]);
    render(<AccessPanel onClose={() => {}} />);

    await waitFor(() => expect(screen.getByText('directory-admins')).toBeTruthy());
    expect(
      screen.getByText('cn=admins,ou=groups,dc=example @ ldaps://directory.example'),
    ).toBeTruthy();
  });

  it('offers an LDAP group as a subject kind', async () => {
    vi.spyOn(bindings, 'fetchRoleBindings').mockResolvedValue([]);
    render(<AccessPanel onClose={() => {}} />);

    await openForm();
    const kinds = Array.from(
      (screen.getByLabelText('Subject') as HTMLSelectElement).options,
      (option) => option.value,
    );
    expect(kinds).toEqual(['oidc_group', 'ldap_group', 'user']);
  });

  it('refuses an issuer whose scheme contradicts the subject kind', async () => {
    vi.spyOn(bindings, 'fetchRoleBindings').mockResolvedValue([]);
    const save = vi.spyOn(bindings, 'saveRoleBinding').mockResolvedValue(undefined);
    render(<AccessPanel onClose={() => {}} />);

    await openForm();
    // An LDAP binding pointed at an https issuer can never match anybody, yet
    // it sits in the list looking like access somebody has — so it is caught
    // while it is still being typed rather than only on the server's 400.
    fireEvent.change(screen.getByLabelText('Subject'), { target: { value: 'ldap_group' } });
    fireEvent.change(screen.getByLabelText('Issuer'), {
      target: { value: 'https://idp.example' },
    });
    fireEvent.change(screen.getByLabelText('Group'), { target: { value: 'admins' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save binding' }));

    await waitFor(() => expect(screen.getByRole('alert').textContent).toMatch(/ldap:\/\//));
    expect(save).not.toHaveBeenCalled();

    // And the same mistake the other way around.
    fireEvent.change(screen.getByLabelText('Subject'), { target: { value: 'oidc_group' } });
    fireEvent.change(screen.getByLabelText('Issuer'), {
      target: { value: 'ldaps://directory.example' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save binding' }));

    await waitFor(() => expect(screen.getByRole('alert').textContent).toMatch(/https:\/\//));
    expect(save).not.toHaveBeenCalled();
  });

  it('accepts each kind with the scheme that belongs to it', async () => {
    vi.spyOn(bindings, 'fetchRoleBindings').mockResolvedValue([]);
    const save = vi.spyOn(bindings, 'saveRoleBinding').mockResolvedValue(undefined);
    render(<AccessPanel onClose={() => {}} />);

    await openForm();
    fireEvent.change(screen.getByLabelText('Subject'), { target: { value: 'ldap_group' } });
    fireEvent.change(screen.getByLabelText('Issuer'), {
      target: { value: 'ldaps://directory.example' },
    });
    fireEvent.change(screen.getByLabelText('Group'), { target: { value: 'admins' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save binding' }));

    await waitFor(() => expect(save).toHaveBeenCalled());
    expect(save.mock.calls[0]?.[0]).toMatchObject({
      subjectKind: 'ldap_group',
      subjectIssuer: 'ldaps://directory.example',
      subjectGroup: 'admins',
    });

    save.mockClear();
    await openForm();
    fireEvent.change(screen.getByLabelText('Subject'), { target: { value: 'oidc_group' } });
    fireEvent.change(screen.getByLabelText('Issuer'), { target: { value: 'https://idp.example' } });
    fireEvent.change(screen.getByLabelText('Group'), { target: { value: 'platform' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save binding' }));

    await waitFor(() => expect(save).toHaveBeenCalled());
    expect(save.mock.calls[0]?.[0]).toMatchObject({
      subjectKind: 'oidc_group',
      subjectIssuer: 'https://idp.example',
    });
  });

  it('will not rename an existing binding, because that is a different one', async () => {
    vi.spyOn(bindings, 'fetchRoleBindings').mockResolvedValue([scoped]);
    render(<AccessPanel onClose={() => {}} />);
    await waitFor(() => expect(screen.getByText('platform')).toBeTruthy());
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    expect((screen.getByLabelText('Name') as HTMLInputElement).disabled).toBe(true);
  });
});
