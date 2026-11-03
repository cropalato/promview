import { describe, expect, it, vi } from 'vitest';
import {
  deleteViewGroup,
  fetchViewGroups,
  joinViewGroup,
  leaveViewGroup,
  pinViewGroup,
  saveViewGroup,
  unpinViewGroup,
} from './viewGroups';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

describe('view groups client', () => {
  it('reads each group with the caller’s own state', async () => {
    const fetchImpl = vi.fn(() =>
      Promise.resolve(
        jsonResponse({
          groups: [
            { name: 'platform', description: 'Platform', joined: true, pinned: false },
            {
              name: 'db',
              description: '',
              joined: false,
              pinned: true,
              matchers: [{ name: 'team', operator: '=', value: 'db' }],
              pins: [
                { subjectKind: 'ldap_group', subjectIssuer: 'ldaps://x', subjectGroup: 'dba' },
              ],
              members: 3,
            },
            { description: 'nameless is dropped' },
          ],
        }),
      ),
    );
    const groups = await fetchViewGroups(fetchImpl);
    expect(fetchImpl).toHaveBeenCalledWith('/api/v1/view-groups', { method: 'GET' });
    expect(groups.map((group) => group.name)).toEqual(['platform', 'db']);
    // A non-administrator never receives selectors; their absence is not an empty selector.
    expect(groups[0]).toMatchObject({ joined: true, pinned: false, matchers: [], pins: [] });
    // An LDAP pin stays an LDAP pin: the label names the directory to an administrator.
    expect(groups[1]?.pins[0]?.subjectKind).toBe('ldap_group');
    expect(groups[1]?.members).toBe(3);
  });

  it('treats a 404 as no groups, which is what open mode answers', async () => {
    const fetchImpl = vi.fn(() => Promise.resolve(jsonResponse({ error: 'unavailable' }, 404)));
    expect(await fetchViewGroups(fetchImpl)).toEqual([]);
  });

  it('still raises other failures', async () => {
    const fetchImpl = vi.fn(() => Promise.resolve(jsonResponse({ error: 'boom' }, 500)));
    await expect(fetchViewGroups(fetchImpl)).rejects.toThrow('boom');
  });

  it('joins and leaves through the membership path, encoding the name', async () => {
    const fetchImpl = vi.fn(() => Promise.resolve(new Response(null, { status: 204 })));
    await joinViewGroup('a b', fetchImpl);
    await leaveViewGroup('a b', fetchImpl);
    expect(fetchImpl).toHaveBeenNthCalledWith(1, '/api/v1/view-groups/a%20b/membership', {
      method: 'PUT',
    });
    expect(fetchImpl).toHaveBeenNthCalledWith(2, '/api/v1/view-groups/a%20b/membership', {
      method: 'DELETE',
    });
  });

  it('surfaces the server’s reason when leaving a pinned group is refused', async () => {
    const fetchImpl = vi.fn(() =>
      Promise.resolve(jsonResponse({ error: 'view group is pinned and cannot be left' }, 409)),
    );
    await expect(leaveViewGroup('db', fetchImpl)).rejects.toThrow(/pinned and cannot be left/);
  });

  it('saves a group with its name in the path alone', async () => {
    const fetchImpl = vi.fn(() => Promise.resolve(jsonResponse({})));
    await saveViewGroup(
      {
        name: 'platform',
        description: 'Platform',
        matchers: [{ name: 'team', operator: '=', value: 'platform' }],
      },
      fetchImpl,
    );
    const [url, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe('/api/v1/view-groups/platform');
    expect(init.method).toBe('PUT');
    expect(JSON.parse(String(init.body))).toEqual({
      description: 'Platform',
      matchers: [{ name: 'team', operator: '=', value: 'platform' }],
    });
  });

  it('removes a group, and pins and unpins by naming the subject in the body', async () => {
    const fetchImpl = vi.fn(() => Promise.resolve(new Response(null, { status: 204 })));
    const pin = { subjectKind: 'user' as const, userID: 4 };
    await deleteViewGroup('platform', fetchImpl);
    await pinViewGroup('platform', pin, fetchImpl);
    await unpinViewGroup('platform', pin, fetchImpl);
    const calls = fetchImpl.mock.calls as unknown as [string, RequestInit][];
    expect(calls[0]?.[1].method).toBe('DELETE');
    expect(calls[1]?.[0]).toBe('/api/v1/view-groups/platform/pins');
    expect(calls[1]?.[1].method).toBe('POST');
    expect(JSON.parse(String(calls[1]?.[1].body))).toEqual(pin);
    expect(calls[2]?.[1].method).toBe('DELETE');
    expect(JSON.parse(String(calls[2]?.[1].body))).toEqual(pin);
  });
});
