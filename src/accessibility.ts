export function isKeyboardActivation(detail: number): boolean {
  return detail === 0;
}

export function isEscapeKey(key: string): boolean {
  return key === 'Escape';
}

export function shouldFocusDetails(source: 'map-pointer' | 'map-keyboard' | 'search'): boolean {
  return source !== 'map-pointer';
}

export type DetailsFocusIntent = { token: number; requested: boolean };

export function nextDetailsFocusIntent(current: DetailsFocusIntent, requested: boolean): DetailsFocusIntent {
  return { token: current.token + 1, requested };
}

export function restoreFocus(element: Pick<HTMLElement, 'focus' | 'isConnected'> | null | undefined): void {
  if (element?.isConnected) element.focus({ preventScroll: true });
}
