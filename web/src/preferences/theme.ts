/**
 * The console palette.
 *
 * Every colour the console renders comes from a custom property declared once
 * at the top of styles/global.css, so a palette is a block of token values and
 * nothing else. `system` declares no block: it leaves the document without a
 * `data-theme` attribute so the stylesheet's `prefers-color-scheme` rule wins,
 * which is exactly what the console did before a palette could be picked.
 *
 * Applying a theme is a document-level side effect rather than a React prop
 * because the tokens are read by the whole stylesheet, including surfaces
 * painted outside the app root (the body background, the browser's own UI via
 * `color-scheme`). Every DOM access is guarded: the console is meant to run in
 * the future Tauri shell and under jsdom too.
 */

export type Theme =
  | 'system'
  | 'dark'
  | 'light'
  | 'nord'
  | 'gruvbox'
  | 'solarized-light'
  | 'high-contrast'
  | 'colorblind-safe';

export interface ThemeOption {
  id: Theme;
  label: string;
}

/**
 * The picker's contents and order. `system` leads because it is the default;
 * the two originals follow; the rest are grouped dark-then-light-then-purpose
 * so the list reads as a progression rather than an inventory.
 */
export const THEMES: readonly ThemeOption[] = [
  { id: 'system', label: 'System' },
  { id: 'dark', label: 'Dark' },
  { id: 'light', label: 'Light' },
  { id: 'nord', label: 'Nord' },
  { id: 'gruvbox', label: 'Gruvbox' },
  { id: 'solarized-light', label: 'Solarized Light' },
  { id: 'high-contrast', label: 'High Contrast' },
  { id: 'colorblind-safe', label: 'Colorblind Safe' },
];

const THEME_IDS: readonly string[] = THEMES.map((theme) => theme.id);

export function isTheme(value: unknown): value is Theme {
  return typeof value === 'string' && THEME_IDS.includes(value);
}

/**
 * Points the browser's own chrome at the palette's background. The two static
 * `theme-color` metas in index.html only know about the OS setting, so a pinned
 * theme would otherwise leave the mobile address bar the wrong colour.
 */
function syncThemeColor(root: HTMLElement): void {
  try {
    const background = getComputedStyle(root).getPropertyValue('--bg').trim();
    if (background === '') {
      return;
    }
    const document_ = root.ownerDocument;
    for (const meta of document_.querySelectorAll('meta[name="theme-color"]')) {
      meta.removeAttribute('media');
      meta.setAttribute('content', background);
    }
  } catch {
    // Computed styles are unavailable in some embedders; the palette itself
    // still applied, and only the browser chrome misses out.
  }
}

/**
 * Applies a theme to the document. Unknown values fall back to `system` rather
 * than leaving a `data-theme` no stylesheet block matches, which would render
 * the console in whatever the cascade happened to leave behind.
 */
export function applyTheme(theme: Theme, root?: HTMLElement): void {
  try {
    const element =
      root ?? (typeof document === 'undefined' ? undefined : document.documentElement);
    if (element === undefined) {
      return;
    }
    if (theme === 'system' || !isTheme(theme)) {
      delete element.dataset.theme;
    } else {
      element.dataset.theme = theme;
    }
    syncThemeColor(element);
  } catch {
    // The console is usable in the default palette; never let a styling side
    // effect take the render down with it.
  }
}
