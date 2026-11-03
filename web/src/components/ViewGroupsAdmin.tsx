import { useCallback, useEffect, useState } from 'react';
import { formatScope, parseScope } from '../access/bindings';
import type { SubjectKind } from '../access/bindings';
import {
  deleteViewGroup,
  fetchViewGroups,
  pinViewGroup,
  saveViewGroup,
  unpinViewGroup,
} from '../access/viewGroups';
import type { ViewGroup, ViewGroupPin } from '../access/viewGroups';

/**
 * Administering view groups and who they are pinned on, beside the bindings.
 *
 * A view group is a convenience and never a permission: nothing here can show a
 * user an alert their binding does not allow. The server is the authority and
 * this view reports what it said, including when it said no.
 */
export function ViewGroupsAdmin() {
  const [groups, setGroups] = useState<ViewGroup[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [editing, setEditing] = useState<ViewGroup | null>(null);
  const [pinning, setPinning] = useState<string | null>(null);

  const reload = useCallback(() => {
    fetchViewGroups()
      .then((loaded) => {
        setGroups(loaded);
        setError(null);
      })
      .catch((cause: unknown) => {
        setError(cause instanceof Error ? cause.message : String(cause));
      });
  }, []);

  useEffect(reload, [reload]);

  const run = (action: Promise<void>, done: string) => {
    setError(null);
    setNotice(null);
    action
      .then(() => {
        setNotice(done);
        reload();
      })
      .catch((cause: unknown) => {
        setError(cause instanceof Error ? cause.message : String(cause));
      });
  };

  return (
    <section className="access-panel" aria-label="View groups">
      <h2 className="access-title">View groups</h2>
      <p className="access-copy">
        A view group is a selector users can join to narrow what their console shows by default. It
        never grants access: a group is always intersected with each user&apos;s scope. Pinning a
        group on a user or a directory group makes it impossible to leave, though anyone can still
        turn their automatic view off to see everything in their scope.
      </p>

      {error !== null ? (
        <p className="access-error" role="alert">
          {error}
        </p>
      ) : null}
      {notice !== null ? (
        <p className="access-note" role="status">
          {notice}
        </p>
      ) : null}

      {groups === null ? (
        <p className="access-copy">Loading view groups…</p>
      ) : groups.length === 0 ? (
        <p className="access-copy">No view groups yet.</p>
      ) : (
        <table className="access-table">
          <caption>View groups ({groups.length})</caption>
          <thead>
            <tr>
              <th scope="col">Name</th>
              <th scope="col">Selector</th>
              <th scope="col">Members</th>
              <th scope="col">Pinned on</th>
              <th scope="col">
                <span className="visually-hidden">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {groups.map((group) => (
              <tr key={group.name}>
                <td className="cell-mono">
                  {group.name}
                  {group.description !== '' ? (
                    <span className="view-menu-note"> · {group.description}</span>
                  ) : null}
                </td>
                <td className="cell-mono">{formatScope(group.matchers)}</td>
                <td>{group.members}</td>
                <td className="cell-mono">
                  {group.pins.length === 0 ? (
                    'nobody'
                  ) : (
                    <ul className="view-menu-columns" aria-label={`Pins on ${group.name}`}>
                      {group.pins.map((pin) => (
                        <li key={describePin(pin)} className="view-menu-column">
                          <span>{describePin(pin)}</span>
                          <button
                            type="button"
                            className="button"
                            aria-label={`Unpin ${describePin(pin)} from ${group.name}`}
                            onClick={() =>
                              run(unpinViewGroup(group.name, pin), `Unpinned ${group.name}.`)
                            }
                          >
                            Unpin
                          </button>
                        </li>
                      ))}
                    </ul>
                  )}
                </td>
                <td className="access-row-actions">
                  <button type="button" className="button" onClick={() => setEditing(group)}>
                    Edit
                  </button>
                  <button type="button" className="button" onClick={() => setPinning(group.name)}>
                    Pin
                  </button>
                  <button
                    type="button"
                    className="button"
                    onClick={() => run(deleteViewGroup(group.name), `Removed ${group.name}.`)}
                  >
                    Remove
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {pinning !== null ? (
        <PinForm
          group={pinning}
          onCancel={() => setPinning(null)}
          onPinned={() => {
            setNotice(`Pinned ${pinning}.`);
            setError(null);
            setPinning(null);
            reload();
          }}
          onError={setError}
        />
      ) : null}

      {editing === null ? (
        <button
          type="button"
          className="button"
          onClick={() =>
            setEditing({
              name: '',
              description: '',
              joined: false,
              pinned: false,
              matchers: [],
              pins: [],
              members: 0,
            })
          }
        >
          Add a view group
        </button>
      ) : (
        <GroupForm
          group={editing}
          onCancel={() => setEditing(null)}
          onSaved={(name) => {
            setEditing(null);
            setNotice(`Saved ${name}.`);
            setError(null);
            reload();
          }}
          onError={setError}
        />
      )}
    </section>
  );
}

function describePin(pin: ViewGroupPin): string {
  if (pin.subjectKind === 'user') {
    return `user ${pin.userID ?? 0}`;
  }
  return `${pin.subjectGroup ?? ''} @ ${pin.subjectIssuer ?? ''}`;
}

function GroupForm({
  group,
  onCancel,
  onSaved,
  onError,
}: {
  group: ViewGroup;
  onCancel: () => void;
  onSaved: (name: string) => void;
  onError: (message: string) => void;
}) {
  const [name, setName] = useState(group.name);
  const [description, setDescription] = useState(group.description);
  const [selector, setSelector] = useState(formatScope(group.matchers));
  const [pending, setPending] = useState(false);

  const submit = () => {
    const matchers = parseScope(selector);
    // A group with no selector is refused by the server because it would read
    // as "everything"; say so here while the box is still in front of them.
    if (matchers === null || matchers.length === 0) {
      onError('Selector must be comma-separated clauses like team=platform or host=~^web');
      return;
    }
    setPending(true);
    saveViewGroup({ name: name.trim(), description: description.trim(), matchers })
      .then(() => {
        setPending(false);
        onSaved(name.trim());
      })
      .catch((cause: unknown) => {
        setPending(false);
        onError(cause instanceof Error ? cause.message : String(cause));
      });
  };

  return (
    <form
      className="access-form"
      aria-label="View group"
      onSubmit={(event) => {
        event.preventDefault();
        submit();
      }}
    >
      <label className="access-field">
        <span>Group name</span>
        <input
          className="access-input"
          value={name}
          // The name is the group's identity, as it is for a binding.
          disabled={group.name !== ''}
          onChange={(event) => setName(event.target.value)}
        />
      </label>
      <label className="access-field">
        <span>Description</span>
        <input
          className="access-input"
          value={description}
          onChange={(event) => setDescription(event.target.value)}
        />
      </label>
      <label className="access-field">
        <span>Selector</span>
        <input
          className="access-input"
          value={selector}
          placeholder="team=platform"
          onChange={(event) => setSelector(event.target.value)}
        />
      </label>
      <div className="access-row-actions">
        <button type="submit" className="button" disabled={pending || name.trim() === ''}>
          {pending ? 'Saving…' : 'Save view group'}
        </button>
        <button type="button" className="button" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </form>
  );
}

function PinForm({
  group,
  onCancel,
  onPinned,
  onError,
}: {
  group: string;
  onCancel: () => void;
  onPinned: () => void;
  onError: (message: string) => void;
}) {
  const [kind, setKind] = useState<SubjectKind>('oidc_group');
  const [userID, setUserID] = useState('');
  const [issuer, setIssuer] = useState('');
  const [directoryGroup, setDirectoryGroup] = useState('');
  const [pending, setPending] = useState(false);

  const submit = () => {
    const pin: ViewGroupPin =
      kind === 'user'
        ? { subjectKind: 'user', userID: Number(userID) }
        : { subjectKind: kind, subjectIssuer: issuer.trim(), subjectGroup: directoryGroup.trim() };
    setPending(true);
    pinViewGroup(group, pin)
      .then(() => {
        setPending(false);
        onPinned();
      })
      .catch((cause: unknown) => {
        setPending(false);
        onError(cause instanceof Error ? cause.message : String(cause));
      });
  };

  return (
    <form
      className="access-form"
      aria-label={`Pin ${group}`}
      onSubmit={(event) => {
        event.preventDefault();
        submit();
      }}
    >
      <label className="access-field">
        <span>Pin on</span>
        <select
          className="access-input"
          value={kind}
          onChange={(event) => setKind(event.target.value as SubjectKind)}
        >
          <option value="oidc_group">OIDC group</option>
          <option value="ldap_group">LDAP group</option>
          <option value="user">Promview user</option>
        </select>
      </label>
      {kind === 'user' ? (
        <label className="access-field">
          <span>User ID</span>
          <input
            className="access-input"
            value={userID}
            onChange={(event) => setUserID(event.target.value)}
          />
        </label>
      ) : (
        <>
          <label className="access-field">
            <span>Issuer</span>
            <input
              className="access-input"
              value={issuer}
              placeholder={
                kind === 'ldap_group'
                  ? 'ldaps://directory.example.com'
                  : 'https://identity.example.com'
              }
              onChange={(event) => setIssuer(event.target.value)}
            />
          </label>
          <label className="access-field">
            <span>Directory group</span>
            <input
              className="access-input"
              value={directoryGroup}
              onChange={(event) => setDirectoryGroup(event.target.value)}
            />
          </label>
        </>
      )}
      <div className="access-row-actions">
        <button type="submit" className="button" disabled={pending}>
          {pending ? 'Pinning…' : `Pin ${group}`}
        </button>
        <button type="button" className="button" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </form>
  );
}
