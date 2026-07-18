export function isKeyboardActivation(detail: number): boolean {
  return detail === 0;
}

export function isEscapeKey(key: string): boolean {
  return key === 'Escape';
}

export function restoreFocus(element: Pick<HTMLElement, 'focus' | 'isConnected'> | null | undefined): void {
  if (element?.isConnected) element.focus({ preventScroll: true });
}
