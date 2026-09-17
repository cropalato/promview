import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { SignInForm } from './SignInForm';

afterEach(() => {
  vi.unstubAllGlobals();
});

function fetchMock(): ReturnType<typeof vi.fn> {
  return globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
}

/** Fills both fields and submits, the way an operator would. */
function submitCredentials(username = 'ada', password = 'correct horse'): void {
  fireEvent.change(screen.getByLabelText('Username'), { target: { value: username } });
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: password } });
  fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
}

function passwordField(): HTMLInputElement {
  return screen.getByLabelText('Password') as HTMLInputElement;
}

describe('SignInForm', () => {
  it('sends the username and password to the login endpoint', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 204 })));
    const onSignedIn = vi.fn();
    render(<SignInForm onSignedIn={onSignedIn} />);

    // The password is masked; a console that echoes it is a console somebody
    // shoulder-surfs during an incident call.
    expect(passwordField().type).toBe('password');

    submitCredentials();

    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock().mock.calls[0] as [string, RequestInit];
    expect(url).toBe('/api/v1/auth/login');
    expect(init.method).toBe('POST');
    expect(JSON.parse(String(init.body))).toEqual({
      username: 'ada',
      password: 'correct horse',
    });
  });

  it('disables the submit control while the request is in flight', async () => {
    let settle = (): void => {};
    vi.stubGlobal(
      'fetch',
      vi.fn().mockReturnValue(
        new Promise<Response>((resolve) => {
          settle = () => resolve(new Response(null, { status: 204 }));
        }),
      ),
    );
    render(<SignInForm onSignedIn={vi.fn()} />);

    submitCredentials();

    const pending = await screen.findByRole('button', { name: 'Signing in…' });
    expect(pending).toBeDisabled();

    settle();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Sign in' })).toBeEnabled());
  });

  it('clears the password from the form once the request settles', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 204 })));
    render(<SignInForm onSignedIn={vi.fn()} />);

    submitCredentials();

    // A password left in state is a password in a heap snapshot and in the
    // devtools tree for as long as the tab is open. The username stays: it is
    // not a secret, and retyping it after a typo in the password is busywork.
    await waitFor(() => expect(passwordField().value).toBe(''));
    expect((screen.getByLabelText('Username') as HTMLInputElement).value).toBe('ada');
  });

  it('clears the password after a refusal too', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response('invalid credentials', { status: 401 })),
    );
    render(<SignInForm onSignedIn={vi.fn()} />);

    submitCredentials();

    await screen.findByRole('alert');
    expect(passwordField().value).toBe('');
  });

  it('says only that the credentials were wrong on a 401', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response('invalid credentials', { status: 401 })),
    );
    const onSignedIn = vi.fn();
    render(<SignInForm onSignedIn={onSignedIn} />);

    submitCredentials();

    // The server refuses an unknown username, a wrong password, a disabled
    // account and a locked one identically; narrowing it here would leak what
    // the server withheld.
    const message = await screen.findByRole('alert');
    expect(message).toHaveTextContent(/incorrect username or password/i);
    expect(message).not.toHaveTextContent(/no such|unknown|disabled|locked/i);
    expect(onSignedIn).not.toHaveBeenCalled();
  });

  it('says how long to wait when the server throttled the attempt', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response('too many sign-in attempts', {
          status: 429,
          headers: { 'Retry-After': '30' },
        }),
      ),
    );
    render(<SignInForm onSignedIn={vi.fn()} />);

    submitCredentials();

    expect(await screen.findByRole('alert')).toHaveTextContent(/try again in 30 seconds/i);
  });

  it('asks for no wait the server never named', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 429 })));
    render(<SignInForm onSignedIn={vi.fn()} />);

    submitCredentials();

    const message = await screen.findByRole('alert');
    expect(message).toHaveTextContent(/too many sign-in attempts/i);
    expect(message).not.toHaveTextContent(/\d+ seconds/);
  });

  it('offers no control the server cannot honour', () => {
    vi.stubGlobal('fetch', vi.fn());
    render(<SignInForm onSignedIn={vi.fn()} />);

    // There is no password reset on the server, and no session longer than the
    // one it issues, so neither is offered here.
    expect(screen.queryByText(/remember me/i)).toBeNull();
    expect(screen.queryByText(/forgot/i)).toBeNull();
  });
});
