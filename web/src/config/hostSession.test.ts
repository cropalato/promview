import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  getHostSignIn,
  installHostSession,
  onHostSessionChange,
  resetHostSession,
} from './hostSession';
import type { HostSessionMessage } from './hostSession';

const DISPATCH_GLOBAL = '__PROMVIEW_SESSION__';

type Dispatch = (message: HostSessionMessage) => void;

function dispatchGlobal(): Dispatch | undefined {
  return (globalThis as Record<string, unknown>)[DISPATCH_GLOBAL] as Dispatch | undefined;
}

afterEach(() => {
  resetHostSession();
});

describe('hostSession', () => {
  it('offers no sign-in in a browser, where no host installed one', () => {
    expect(getHostSignIn()).toBeUndefined();
    expect(dispatchGlobal()).toBeUndefined();
  });

  it('signs in through the host and resolves once the session is stored', async () => {
    const invoke = vi.fn().mockResolvedValue('keychain');
    installHostSession(invoke);

    const signIn = getHostSignIn();
    expect(signIn).toBeDefined();
    await signIn?.();

    expect(invoke).toHaveBeenCalledWith('sign_in');
  });

  it('surfaces the host refusing to sign in', async () => {
    const invoke = vi.fn().mockRejectedValue(new Error('timed out'));
    installHostSession(invoke);

    await expect(getHostSignIn()?.()).rejects.toThrow('timed out');
  });

  it('delivers host announcements to every listener until it unsubscribes', () => {
    installHostSession(vi.fn());
    const first = vi.fn();
    const second = vi.fn();
    const unsubscribe = onHostSessionChange(first);
    onHostSessionChange(second);

    dispatchGlobal()?.({ kind: 'signedIn' });
    expect(first).toHaveBeenCalledWith({ kind: 'signedIn' });
    expect(second).toHaveBeenCalledWith({ kind: 'signedIn' });

    unsubscribe();
    dispatchGlobal()?.({ kind: 'signedOut' });
    expect(first).toHaveBeenCalledTimes(1);
    expect(second).toHaveBeenCalledWith({ kind: 'signedOut' });
  });
});
