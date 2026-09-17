import { useCallback, useEffect, useState } from 'react';
import {
  deleteRoleBinding,
  fetchRoleBindings,
  formatScope,
  parseScope,
  saveRoleBinding,
} from '../access/bindings';
import type { BindingRole, RoleBinding, SubjectKind } from '../access/bindings';

/**
 * Administering who can do what, for administrators.
 *
 * The server is the authority: it refuses an operator, refuses open mode, and
 * refuses a change that would leave nobody able to administer the deployment.
 * This view offers the controls and reports what the server said, including
 * when what it said was no.
 */
export function AccessPanel({ onClose }: { onClose: () => void }) {
  const [bindings, setBindings] = useState<RoleBinding[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [editing, setEditing] = useState<RoleBinding | null>(null);

  const reload = useCallback(() => {
    fetchRoleBindings()
      .then((loaded) => {
        setBindings(loaded);
        setError(null);
      })
      .catch((cause: unknown) => {
        setError(cause instanceof Error ? cause.message : String(cause));
      });
  }, []);

  useEffect(reload, [reload]);

  const remove = (name: string) => {
    setError(null);
    setNotice(null);
    deleteRoleBinding(name)
      .then(() => {
        setNotice(`Removed ${name}.`);
        reload();
      })
      .catch((cause: unknown) => {
        // The refusal to remove the last administrator arrives here, and it is
        // guidance rather than a fault: an administrator who saw only a status
        // code would have no idea the rule existed.
        setError(cause instanceof Error ? cause.message : String(cause));
      });
  };

  return (
    <section className="access-panel" aria-label="Access">
      <header className="access-head">
        <h2 className="access-title">Access</h2>
        <button type="button" className="button" onClick={onClose}>
          Back to alerts
        </button>
      </header>

      <p className="access-copy">
        A binding grants a role to an OIDC group or a user, optionally narrowed to a label scope.
        Roles and scopes are enforced in SQL on every query, count and stream, so a scoped viewer
        cannot see outside it. Changes take effect on existing sessions immediately.
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

      {bindings === null ? (
        <p className="access-copy">Loading bindings…</p>
      ) : bindings.length === 0 ? (
        <p className="access-copy">
          No bindings yet. Until one exists, every signed-in identity is denied: Promview never
          infers a role from a provider claim.
        </p>
      ) : (
        <table className="access-table">
          <caption>Role bindings ({bindings.length})</caption>
          <thead>
            <tr>
              <th scope="col">Name</th>
              <th scope="col">Subject</th>
              <th scope="col">Role</th>
              <th scope="col">Scope</th>
              <th scope="col">
                <span className="visually-hidden">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {bindings.map((binding) => (
              <tr key={binding.name}>
                <td className="cell-mono">{binding.name}</td>
                <td className="cell-mono">{describeSubject(binding)}</td>
                <td>{binding.role}</td>
                {/* The scope, not just the role: a viewer bound with
                    team=platform sees a different deployment than one bound
                    without it. */}
                <td className="cell-mono">
                  {binding.matchers.length === 0 ? 'everything' : formatScope(binding.matchers)}
                </td>
                <td className="access-row-actions">
                  <button type="button" className="button" onClick={() => setEditing(binding)}>
                    Edit
                  </button>
                  <button type="button" className="button" onClick={() => remove(binding.name)}>
                    Remove
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {editing === null ? (
        <button type="button" className="button" onClick={() => setEditing(emptyBinding())}>
          Add a binding
        </button>
      ) : (
        <BindingForm
          binding={editing}
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

function emptyBinding(): RoleBinding {
  return { name: '', subjectKind: 'oidc_group', role: 'viewer', matchers: [] };
}

function describeSubject(binding: RoleBinding): string {
  if (binding.subjectKind === 'user') {
    return `user ${binding.userID ?? 0}`;
  }
  return `${binding.oidcGroup ?? ''} @ ${binding.oidcIssuer ?? ''}`;
}

function BindingForm({
  binding,
  onCancel,
  onSaved,
  onError,
}: {
  binding: RoleBinding;
  onCancel: () => void;
  onSaved: (name: string) => void;
  onError: (message: string) => void;
}) {
  const [name, setName] = useState(binding.name);
  const [subjectKind, setSubjectKind] = useState<SubjectKind>(binding.subjectKind);
  const [issuer, setIssuer] = useState(binding.oidcIssuer ?? '');
  const [group, setGroup] = useState(binding.oidcGroup ?? '');
  const [userID, setUserID] = useState(String(binding.userID ?? ''));
  const [role, setRole] = useState<BindingRole>(binding.role);
  const [scope, setScope] = useState(formatScope(binding.matchers));
  const [pending, setPending] = useState(false);

  const submit = () => {
    const matchers = parseScope(scope);
    if (matchers === null) {
      onError('Scope must be comma-separated clauses like team=platform or host=~^web');
      return;
    }
    setPending(true);
    saveRoleBinding({
      name: name.trim(),
      subjectKind,
      role,
      matchers,
      ...(subjectKind === 'user'
        ? { userID: Number(userID) }
        : { oidcIssuer: issuer.trim(), oidcGroup: group.trim() }),
    })
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
      onSubmit={(event) => {
        event.preventDefault();
        submit();
      }}
    >
      <label className="access-field">
        <span>Name</span>
        <input
          className="access-input"
          value={name}
          // The name is the binding's identity, so an existing one keeps it:
          // renaming is creating a different binding and removing this one.
          disabled={binding.name !== ''}
          onChange={(event) => setName(event.target.value)}
        />
      </label>
      <label className="access-field">
        <span>Subject</span>
        <select
          className="access-input"
          value={subjectKind}
          onChange={(event) => setSubjectKind(event.target.value as SubjectKind)}
        >
          <option value="oidc_group">OIDC group</option>
          <option value="user">Promview user</option>
        </select>
      </label>
      {subjectKind === 'user' ? (
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
              placeholder="https://identity.example.com"
              onChange={(event) => setIssuer(event.target.value)}
            />
          </label>
          <label className="access-field">
            <span>Group</span>
            <input
              className="access-input"
              value={group}
              onChange={(event) => setGroup(event.target.value)}
            />
          </label>
        </>
      )}
      <label className="access-field">
        <span>Role</span>
        <select
          className="access-input"
          value={role}
          onChange={(event) => setRole(event.target.value as BindingRole)}
        >
          <option value="viewer">viewer</option>
          <option value="operator">operator</option>
          <option value="administrator">administrator</option>
        </select>
      </label>
      <label className="access-field">
        <span>Scope</span>
        <input
          className="access-input"
          value={scope}
          placeholder="team=platform, environment!=development"
          onChange={(event) => setScope(event.target.value)}
        />
      </label>
      <div className="access-row-actions">
        <button type="submit" className="button" disabled={pending || name.trim() === ''}>
          {pending ? 'Saving…' : 'Save binding'}
        </button>
        <button type="button" className="button" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </form>
  );
}
