import { describe, expect, it, vi } from 'vitest';
import { createHostNotificationFactory } from './hostNotifications';

describe('host notifications', () => {
  it('asks the host to show one, passing the body through', () => {
    const invoke = vi.fn().mockResolvedValue(undefined);
    createHostNotificationFactory(invoke).create('Critical: HighCPU', {
      body: 'CPU above 95%\nam-eu · core',
      tag: 'promview-alert-42',
    });

    expect(invoke).toHaveBeenCalledWith('show_notification', {
      title: 'Critical: HighCPU',
      body: 'CPU above 95%\nam-eu · core',
    });
  });

  it('sends an empty body rather than undefined when there is none', () => {
    const invoke = vi.fn().mockResolvedValue(undefined);
    createHostNotificationFactory(invoke).create('Critical: HighCPU', {});

    const [, payload] = invoke.mock.calls[0] as [string, { body: string }];
    expect(payload.body).toBe('');
  });

  it('reports permission as already granted, since the host is the application', () => {
    // There is nothing to prompt for: the operating system settled this at
    // install time, and the console must not sit in a "disabled" state waiting
    // for a prompt that will never come.
    const factory = createHostNotificationFactory(vi.fn());
    expect(factory.permission).toBe('granted');
    return expect(factory.requestPermission()).resolves.toBe('granted');
  });

  it('survives a host that refuses to show one', async () => {
    // The alert is in the console either way; a failed notification must not
    // take the stream handler down with it.
    const invoke = vi.fn().mockRejectedValue(new Error('no notification daemon'));
    const factory = createHostNotificationFactory(invoke);

    expect(() => factory.create('Critical: HighCPU', { body: 'x' })).not.toThrow();
    await Promise.resolve();
    await Promise.resolve();
  });

  it('returns a handle the console can hold without it doing anything', () => {
    const handle = createHostNotificationFactory(vi.fn()).create('t', {});
    // Click-to-deep-link is not wired yet; the handle satisfies the contract
    // and nothing more.
    expect(handle.onclick).toBeNull();
    expect(() => handle.close()).not.toThrow();
  });
});
