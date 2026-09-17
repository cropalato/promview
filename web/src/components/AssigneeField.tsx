import { useEffect, useState } from 'react';

interface AssigneeFieldProps {
  assignee: string;
  onAssign: (assignee: string) => Promise<void>;
}

/**
 * Records who owns an alert.
 *
 * Free text rather than a picker of known users: an alert is routinely handed
 * to somebody who has never signed in here — a vendor, a team rota address, the
 * name in a runbook — and a list of accounts would turn every one of those into
 * something the operator cannot express.
 *
 * Clearing is submitting an empty value. There is no separate unassign control
 * because the API has no separate verb: the request states the assignment in
 * full, so the field is the whole interface.
 */
export function AssigneeField({ assignee, onAssign }: AssigneeFieldProps) {
  const [value, setValue] = useState(assignee);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // The drawer is remounted per alert id, but the assignment can also change
  // under it — another operator, or a reopen clearing it — so the field follows
  // the server rather than keeping a stale local edit.
  useEffect(() => {
    setValue(assignee);
  }, [assignee]);

  const submit = (next: string) => {
    if (next === assignee) {
      return;
    }
    setPending(true);
    setError(null);
    onAssign(next)
      .then(() => {
        setPending(false);
      })
      .catch((cause: unknown) => {
        setPending(false);
        setError(cause instanceof Error ? cause.message : String(cause));
      });
  };

  return (
    <div className="detail-action">
      <form
        className="detail-assignee"
        onSubmit={(event) => {
          event.preventDefault();
          submit(value.trim());
        }}
      >
        <label className="detail-assignee-label" htmlFor="alert-assignee">
          Assignee
        </label>
        <input
          id="alert-assignee"
          className="detail-assignee-input"
          type="text"
          value={value}
          placeholder="nobody"
          disabled={pending}
          onChange={(event) => setValue(event.target.value)}
        />
        <button type="submit" className="button" disabled={pending} aria-busy={pending}>
          {pending ? 'Saving…' : 'Save'}
        </button>
        {assignee !== '' ? (
          <button
            type="button"
            className="button"
            disabled={pending}
            onClick={() => {
              setValue('');
              submit('');
            }}
          >
            Clear
          </button>
        ) : null}
      </form>
      {error !== null ? (
        <p className="detail-action-error" role="alert">
          {error}
        </p>
      ) : null}
    </div>
  );
}
