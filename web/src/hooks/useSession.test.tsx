import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useSession } from './useSession';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const PRINCIPAL = {
  subject: 'https://idp.example|user-1',
  email: 'ada@example.com',
  displayName: 'Ada Lovelace',
  roles: ['operator'],
  anonymous: false,
};

describe('useSession', () => {
  it('stays idle without a /me request while the auth mode is unknown', async () => {
    const fetchImpl = vi.fn();
    const { result } = renderHook(() => useSession(undefined, { fetchImpl }));

    await act(async () => {});

    expect(result.current.state).toEqual({ status: 'idle' });
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it('stays idle without a /me request in open mode', async () => {
    const fetchImpl = vi.fn();
    const { result } = renderHook(() => useSession('open', { fetchImpl }));

    await act(async () => {});

    expect(result.current.state).toEqual({ status: 'idle' });
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it('stays idle without a /me request in ldap mode', async () => {
    const fetchImpl = vi.fn();
    const { result } = renderHook(() => useSession('ldap', { fetchImpl }));

    await act(async () => {});

    expect(result.current.state).toEqual({ status: 'idle' });
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it('verifies the session in oidc mode', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse(PRINCIPAL));
    const { result } = renderHook(() => useSession('oidc', { fetchImpl }));

    expect(result.current.state).toEqual({ status: 'loading' });
    await waitFor(() => expect(result.current.state.status).toBe('ready'));

    expect(fetchImpl).toHaveBeenCalledTimes(1);
    expect(fetchImpl).toHaveBeenCalledWith('/api/v1/me');
    expect(result.current.state).toEqual({
      status: 'ready',
      session: {
        subject: 'https://idp.example|user-1',
        email: 'ada@example.com',
        displayName: 'Ada Lovelace',
        roles: ['operator'],
        anonymous: false,
      },
    });
  });

  it('maps a 401 to the unauthenticated gate', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse({ error: 'nope' }, 401));
    const { result } = renderHook(() => useSession('oidc', { fetchImpl }));

    await waitFor(() => expect(result.current.state).toEqual({ status: 'unauthenticated' }));
  });

  it('maps a 403 to the forbidden gate', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse({ error: 'nope' }, 403));
    const { result } = renderHook(() => useSession('oidc', { fetchImpl }));

    await waitFor(() => expect(result.current.state).toEqual({ status: 'forbidden' }));
  });

  it('surfaces request errors and retries the session check', async () => {
    const fetchImpl = vi
      .fn()
      .mockRejectedValueOnce(new TypeError('fetch failed'))
      .mockResolvedValue(jsonResponse(PRINCIPAL));
    const { result } = renderHook(() => useSession('oidc', { fetchImpl }));

    await waitFor(() => expect(result.current.state.status).toBe('error'));

    act(() => result.current.retry());

    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    expect(fetchImpl).toHaveBeenCalledTimes(2);
  });

  it('drops a verified session back to the unauthenticated gate on expire', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse(PRINCIPAL));
    const { result } = renderHook(() => useSession('oidc', { fetchImpl }));

    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    act(() => result.current.expire());

    expect(result.current.state).toEqual({ status: 'unauthenticated' });
  });

  it('ignores expire when no session is verified', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(jsonResponse({ error: 'nope' }, 401));
    const { result } = renderHook(() => useSession('oidc', { fetchImpl }));

    await waitFor(() => expect(result.current.state).toEqual({ status: 'unauthenticated' }));
    act(() => result.current.expire());

    expect(result.current.state).toEqual({ status: 'unauthenticated' });
  });

  it('signs out through the logout endpoint and navigates home', async () => {
    const fetchImpl = vi
      .fn()
      .mockImplementation((url: string) =>
        Promise.resolve(
          url === '/api/v1/auth/logout'
            ? new Response(null, { status: 204 })
            : jsonResponse(PRINCIPAL),
        ),
      );
    const navigate = vi.fn();
    const { result } = renderHook(() => useSession('oidc', { fetchImpl, navigate }));

    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    act(() => result.current.signOut());

    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/'));
    expect(fetchImpl).toHaveBeenCalledWith('/api/v1/auth/logout', { method: 'POST' });
  });

  it('keeps the session and reports the failure when sign-out fails', async () => {
    const fetchImpl = vi
      .fn()
      .mockImplementation((url: string) =>
        Promise.resolve(
          url === '/api/v1/auth/logout'
            ? jsonResponse({ error: 'boom' }, 500)
            : jsonResponse(PRINCIPAL),
        ),
      );
    const navigate = vi.fn();
    const { result } = renderHook(() => useSession('oidc', { fetchImpl, navigate }));

    await waitFor(() => expect(result.current.state.status).toBe('ready'));
    act(() => result.current.signOut());

    await waitFor(() => expect(result.current.signOutState).toBe('error'));
    expect(result.current.state.status).toBe('ready');
    expect(navigate).not.toHaveBeenCalled();
  });
});
