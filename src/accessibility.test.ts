import { describe, expect, it, vi } from 'vitest';
import { isEscapeKey, isKeyboardActivation, restoreFocus } from './accessibility';

describe('accessible disclosure focus', () => {
  it('distinguishes keyboard activation from pointer clicks', () => {
    expect(isKeyboardActivation(0)).toBe(true);
    expect(isKeyboardActivation(1)).toBe(false);
  });

  it('recognises only the Escape key as a close command', () => {
    expect(isEscapeKey('Escape')).toBe(true);
    expect(isEscapeKey('Enter')).toBe(false);
    expect(isEscapeKey('Esc')).toBe(false);
  });

  it('restores focus without scrolling when the origin remains connected', () => {
    const focus = vi.fn();
    restoreFocus({ isConnected: true, focus });
    expect(focus).toHaveBeenCalledWith({ preventScroll: true });
  });

  it('does not focus a removed origin', () => {
    const focus = vi.fn();
    restoreFocus({ isConnected: false, focus });
    expect(focus).not.toHaveBeenCalled();
  });
});
