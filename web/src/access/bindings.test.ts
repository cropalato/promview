import { describe, expect, it, vi } from 'vitest';
import {
  deleteRoleBinding,
  fetchRoleBindings,
  formatScope,
  parseScope,
  saveRoleBinding,
} from './bindings';
import type { RoleBinding } from './bindings';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const binding: RoleBinding = {
  name: 'platform',
  subjectKind: 'oidc_group',
  subjectIssuer: 'https://idp.example',
  subjectGroup: 'platform',
  role: 'operator',
  matchers: [{ name: 'team', operator: '=', value: 'platform' }],
};

describe('role bindings', () => {
  it('reads bindings with their scopes', async () => {
    const fetchImpl = vi.fn(() => Promise.resolve(jsonResponse({ bindings: [binding] })));
    const bindings = await fetchRoleBindings(fetchImpl);
    expect(fetchImpl).toHaveBeenCalledWith('/api/v1/access/bindings', { method: 'GET' });
    // The scope is half of what a binding means; a list without it would
    // describe a different binding than the one in force.
    expect(bindings[0]?.matchers).toEqual([{ name: 'team', operator: '=', value: 'platform' }]);
  });

  it('keeps an LDAP group binding labelled as one', async () => {
    // An LDAP binding flattened into oidc_group would read as access through
    // the identity provider, which is the wrong directory and the wrong answer
    // to whether the binding is doing anything at all.
    const fetchImpl = vi.fn(() =>
      Promise.resolve(
        jsonResponse({
          bindings: [
            {
              name: 'directory-admins',
              subjectKind: 'ldap_group',
              subjectIssuer: 'ldaps://directory.example',
              subjectGroup: 'cn=admins,ou=groups,dc=example',
              role: 'administrator',
              matchers: [],
            },
          ],
        }),
      ),
    );
    const bindings = await fetchRoleBindings(fetchImpl);
    expect(bindings[0]?.subjectKind).toBe('ldap_group');
    expect(bindings[0]?.subjectIssuer).toBe('ldaps://directory.example');
    expect(bindings[0]?.subjectGroup).toBe('cn=admins,ou=groups,dc=example');
  });

  it('drops malformed entries rather than inventing bindings', async () => {
    const fetchImpl = vi.fn(() =>
      Promise.resolve(
        jsonResponse({
          bindings: [binding, { name: 'no-role' }, { role: 'viewer' }, 'nonsense'],
        }),
      ),
    );
    const bindings = await fetchRoleBindings(fetchImpl);
    expect(bindings.map((entry) => entry.name)).toEqual(['platform']);
  });

  it('ignores the pre-rename subject keys instead of half-reading them', async () => {
    // A server still sending `oidcIssuer`/`oidcGroup` is older than the binary
    // that serves this console, so it is not one we are paired with. Reading
    // those keys anyway would draw a binding with a blank subject and
    // misdescribe who actually has access.
    const fetchImpl = vi.fn(() =>
      Promise.resolve(
        jsonResponse({
          bindings: [
            {
              name: 'platform',
              subjectKind: 'oidc_group',
              oidcIssuer: 'https://idp.example',
              oidcGroup: 'platform',
              role: 'operator',
              matchers: [],
            },
          ],
        }),
      ),
    );
    const bindings = await fetchRoleBindings(fetchImpl);
    expect(bindings[0]?.subjectIssuer).toBeUndefined();
    expect(bindings[0]?.subjectGroup).toBeUndefined();
  });

  it('sends the name in the path only, because the server refuses a mismatch', async () => {
    const fetchImpl = vi.fn(() => Promise.resolve(new Response(null, { status: 200 })));
    await saveRoleBinding(binding, fetchImpl);
    const [url, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe('/api/v1/access/bindings/platform');
    expect(init.method).toBe('PUT');
    expect(JSON.parse(String(init.body))).not.toHaveProperty('name');
  });

  it('deletes by name', async () => {
    const fetchImpl = vi.fn(() => Promise.resolve(new Response(null, { status: 204 })));
    await deleteRoleBinding('platform', fetchImpl);
    expect(fetchImpl).toHaveBeenCalledWith('/api/v1/access/bindings/platform', {
      method: 'DELETE',
    });
  });

  it('surfaces the reason the server gave, not just the status', async () => {
    // Refusing to remove the last administrator is guidance, and an operator
    // who saw only "HTTP 400" would have no idea what to do about it.
    const fetchImpl = vi.fn(() =>
      Promise.resolve(
        jsonResponse({ error: 'refusing to remove the last administrator binding' }, 400),
      ),
    );
    await expect(deleteRoleBinding('admins', fetchImpl)).rejects.toThrow(/last administrator/);
  });

  it('falls back to the status when there is no reason to read', async () => {
    const fetchImpl = vi.fn(() => Promise.resolve(new Response('not json', { status: 500 })));
    await expect(fetchRoleBindings(fetchImpl)).rejects.toThrow(/HTTP 500/);
  });
});

describe('scope parsing', () => {
  it('keeps all four operators, not just the filter bar pair', () => {
    // A binding written through the CLI can carry a regex scope. An editor that
    // understood four operators as two would silently narrow somebody's scope.
    expect(parseScope('team=platform, env!=dev, host=~^web, zone!~^eu')).toEqual([
      { name: 'team', operator: '=', value: 'platform' },
      { name: 'env', operator: '!=', value: 'dev' },
      { name: 'host', operator: '=~', value: '^web' },
      { name: 'zone', operator: '!~', value: '^eu' },
    ]);
  });

  it('reads the longer operator first', () => {
    expect(parseScope('team!=core')).toEqual([{ name: 'team', operator: '!=', value: 'core' }]);
    expect(parseScope('team=~core')).toEqual([{ name: 'team', operator: '=~', value: 'core' }]);
  });

  it('is empty for an unscoped binding', () => {
    expect(parseScope('   ')).toEqual([]);
  });

  it('refuses a partial scope rather than returning half of one', () => {
    // Half a scope is a different binding, not a smaller one.
    expect(parseScope('team')).toBeNull();
    expect(parseScope('team=')).toBeNull();
    expect(parseScope('=platform')).toBeNull();
    expect(parseScope('team=platform, broken')).toBeNull();
  });

  it('round-trips through the display form', () => {
    const text = 'team=platform, host=~^web';
    expect(formatScope(parseScope(text) ?? [])).toBe(text);
  });
});
