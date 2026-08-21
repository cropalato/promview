import { describe, expect, it } from 'vitest';
import { THEMES, applyTheme, isTheme } from './theme';

function root(): HTMLElement {
  return document.createElement('html');
}

describe('theme', () => {
  it('pins a chosen palette on the document', () => {
    const element = root();
    applyTheme('nord', element);
    expect(element.dataset.theme).toBe('nord');
    applyTheme('high-contrast', element);
    expect(element.dataset.theme).toBe('high-contrast');
  });

  it('leaves no attribute for system so the OS setting decides', () => {
    // The stylesheet's prefers-color-scheme rule is scoped to
    // :root:not([data-theme]); an attribute of any value would beat it.
    const element = root();
    applyTheme('dark', element);
    applyTheme('system', element);
    expect(element.dataset.theme).toBeUndefined();
    expect(element.hasAttribute('data-theme')).toBe(false);
  });

  it('falls back to system rather than pinning a palette no block matches', () => {
    const element = root();
    applyTheme('nord', element);
    applyTheme('neon' as never, element);
    expect(element.hasAttribute('data-theme')).toBe(false);
  });

  it('recognises exactly the palettes the picker offers', () => {
    for (const option of THEMES) {
      expect(isTheme(option.id)).toBe(true);
    }
    expect(isTheme('neon')).toBe(false);
    expect(isTheme('')).toBe(false);
    expect(isTheme(undefined)).toBe(false);
    expect(isTheme(7)).toBe(false);
  });

  it('offers system first, since it is the default', () => {
    expect(THEMES[0]?.id).toBe('system');
  });
});
