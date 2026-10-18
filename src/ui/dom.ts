// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// Plain DOM helpers. Everything on screen is built from these.

export type Child = Node | string | number | null | false | undefined;

export function el<K extends keyof HTMLElementTagNameMap>(tag: K, className?: string, ...children: Child[]): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (className) node.className = className;
  for (const child of children) {
    if (child === null || child === false || child === undefined) continue;
    node.append(typeof child === 'number' ? String(child) : child);
  }
  return node;
}

export function button(className: string, label: Child, onClick: () => void, ariaLabel?: string): HTMLButtonElement {
  const node = el('button', className, label);
  node.type = 'button';
  node.onclick = onClick;
  if (ariaLabel) node.setAttribute('aria-label', ariaLabel);
  return node;
}

export function swatch(colour: string, className = 'swatch'): HTMLSpanElement {
  const node = el('span', className);
  node.style.background = colour;
  node.setAttribute('aria-hidden', 'true');
  return node;
}

// One icon family: 24-unit drawings shown at 18 px, 1.8 px strokes, round ends.
const icon = (body: string, filled = false) => `<svg class="icon" width="18" height="18" viewBox="0 0 24 24" fill="${filled ? 'currentColor' : 'none'}" stroke="${filled ? 'none' : 'currentColor'}" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${body}</svg>`;
export const closeIcon = icon('<path d="M6 6l12 12M18 6 6 18"/>');
export const locateIcon = icon('<path d="M20 4 4 10.6l6.6 2.8L13.4 20z"/>');
export const searchIcon = icon('<circle cx="10.5" cy="10.5" r="6"/><path d="m15 15 5 5"/>');
export const chevronIcon = icon('<path d="m6 9 6 6 6-6"/>');
export const tasteIcon = icon('<path d="M4 7h10M18 7h2M4 17h2M10 17h10"/><circle cx="16" cy="7" r="2"/><circle cx="8" cy="17" r="2"/>');
export const shareIcon = icon('<path d="M12 15V4M8 8l4-4 4 4M5 13v6a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-6"/>');
