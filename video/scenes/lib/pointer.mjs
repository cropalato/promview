// A pointer the viewer can follow. Chromium under Xvfb has no cursor theme
// worth filming, so the cursor is drawn by the page itself: an arrow that
// moves on an ease-out curve and ripples on click. The real mouse moves
// underneath it, so hover states and clicks are the console's own.

import { sleep } from './beat.mjs';

const CURSOR_SVG = `
<svg xmlns="http://www.w3.org/2000/svg" width="24" height="28" viewBox="0 0 24 28">
  <path d="M3 2 L3 22 L8.5 17 L12 25 L15.5 23.5 L12 15.5 L19 15.5 Z"
        fill="#eceff4" stroke="#0b0f14" stroke-width="1.5" stroke-linejoin="round"/>
</svg>`;

const INSTALL = `
(() => {
  if (document.getElementById('__cursor')) return;
  const root = document.createElement('div');
  root.id = '__cursor';
  root.style.cssText = 'position:fixed;left:0;top:0;width:24px;height:28px;pointer-events:none;z-index:2147483647;transform:translate(960px,540px);filter:drop-shadow(0 1px 2px rgba(0,0,0,.6))';
  root.innerHTML = ${JSON.stringify(CURSOR_SVG)};
  document.documentElement.appendChild(root);
  const style = document.createElement('style');
  style.textContent = '@keyframes __ripple{from{transform:translate(-50%,-50%) scale(.2);opacity:.8}to{transform:translate(-50%,-50%) scale(1);opacity:0}}';
  document.head.appendChild(style);
})();`;

const easeOutCubic = (t) => 1 - (1 - t) ** 3;

export class Pointer {
  constructor(page, { x = 960, y = 540 } = {}) {
    this.page = page;
    this.x = x;
    this.y = y;
  }

  async install() {
    await this.page.evaluate(INSTALL);
    await this.page.mouse.move(this.x, this.y);
    await this.#draw();
  }

  async #draw() {
    await this.page.evaluate(
      ([x, y]) => {
        const el = document.getElementById('__cursor');
        if (el) el.style.transform = `translate(${x - 3}px, ${y - 2}px)`;
      },
      [this.x, this.y],
    );
  }

  /** Moves to a point over `ms`, easing out so it reads as a hand settling. */
  async moveTo(x, y, { ms = 600 } = {}) {
    const x0 = this.x;
    const y0 = this.y;
    const frames = Math.max(1, Math.round(ms / 16));
    const started = Date.now();
    for (let i = 1; i <= frames; i += 1) {
      const t = easeOutCubic(i / frames);
      this.x = x0 + (x - x0) * t;
      this.y = y0 + (y - y0) * t;
      await this.page.mouse.move(this.x, this.y);
      await this.#draw();
      await sleep(started + (i * ms) / frames - Date.now());
    }
  }

  /** Moves to the centre of an element. */
  async moveToElement(locator, { ms = 600, dx = 0, dy = 0 } = {}) {
    const box = await locator.boundingBox();
    if (!box) throw new Error('element has no box to move to');
    await this.moveTo(box.x + box.width / 2 + dx, box.y + box.height / 2 + dy, { ms });
  }

  async click() {
    await this.page.evaluate(
      ([x, y]) => {
        const ripple = document.createElement('div');
        ripple.style.cssText = `position:fixed;left:${x}px;top:${y}px;width:36px;height:36px;border-radius:50%;border:2px solid #88c0d0;pointer-events:none;z-index:2147483646;animation:__ripple .35s ease-out forwards`;
        document.documentElement.appendChild(ripple);
        setTimeout(() => ripple.remove(), 400);
      },
      [this.x, this.y],
    );
    await this.page.mouse.down();
    await sleep(60);
    await this.page.mouse.up();
  }

  async clickOn(locator, options) {
    await this.moveToElement(locator, options);
    await this.click();
  }
}
