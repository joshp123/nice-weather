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

export const closeIcon = '<svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><path d="M2 2l10 10M12 2L2 12"/></svg>';
export const locateIcon = '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M21 3 3 10.5l7.2 2.3L12.5 20z"/></svg>';
export const searchIcon = '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" aria-hidden="true"><circle cx="10.5" cy="10.5" r="6.5"/><path d="M15.5 15.5 20 20"/></svg>';
