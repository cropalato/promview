import { useState } from 'react';
import { SessionError, signIn } from '../auth/session';

/**
 * Username and password sign-in, for deployments that keep their own accounts.
 *
 * The password lives in component state only as long as the request does: it
 * is cleared the moment the request settles, success or failure, because a
 * password left in state is a password in a heap snapshot and in the React
 * devtools tree for as long as the tab is open.
 *
 * There is no remember-me, no strength meter and no forgot-password link. The
 * server has no reset flow to point one at, and a control that cannot work is
 * worse than its absence.
 */
export function SignInForm({ onSignedIn }: { onSignedIn: () => void }) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = () => {
    setPending(true);
    setError(null);
    signIn({ username, password })
      .then(() => {
        setPending(false);
        setPassword('');
        // The cookie is set, but only the server can say what it is worth, so
        // the console re-checks the session rather than declaring itself in.
        onSignedIn();
      })
      .catch((cause: unknown) => {
        setPending(false);
        setPassword('');
        setError(describeFailure(cause));
      });
  };

  return (
    <form
      className="signin-form"
      aria-label="Sign in"
      onSubmit={(event) => {
        event.preventDefault();
        submit();
      }}
    >
      <label className="access-field">
        <span>Username</span>
        <input
          className="access-input"
          name="username"
          autoComplete="username"
          required
          value={username}
          onChange={(event) => setUsername(event.target.value)}
        />
      </label>
      <label className="access-field">
        <span>Password</span>
        <input
          className="access-input"
          type="password"
          name="password"
          autoComplete="current-password"
          required
          value={password}
          onChange={(event) => setPassword(event.target.value)}
        />
      </label>
      {error !== null ? (
        <p className="access-error" role="alert">
          {error}
        </p>
      ) : null}
      <button type="submit" className="button" disabled={pending}>
        {pending ? 'Signing in…' : 'Sign in'}
      </button>
    </form>
  );
}

/**
 * What to put in front of the operator.
 *
 * A refusal is shown exactly as coarsely as the server meant it: one message
 * covers a wrong password, an unknown username, a disabled account and a
 * locked one, and narrowing it here would leak what the server withheld. A
 * throttled attempt is the one case with something useful to add, and only
 * when the server said how long.
 */
function describeFailure(cause: unknown): string {
  if (cause instanceof SessionError && cause.status === 429) {
    return cause.retryAfterSeconds === undefined
      ? 'Too many sign-in attempts. Wait a moment and try again.'
      : `Too many sign-in attempts. Try again in ${cause.retryAfterSeconds} seconds.`;
  }
  return cause instanceof Error ? cause.message : String(cause);
}
