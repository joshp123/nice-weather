import { describe, expect, it, vi } from 'vitest';
import { isEscapeKey, isKeyboardActivation, nextDetailsFocusIntent, restoreFocus, shouldFocusDetails } from './accessibility';

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

  it('focuses details for keyboard and search selections but not map pointer clicks', () => {
    expect(shouldFocusDetails('map-keyboard')).toBe(true);
    expect(shouldFocusDetails('search')).toBe(true);
    expect(shouldFocusDetails('map-pointer')).toBe(false);
  });

  it('does not carry keyboard focus intent into a later pointer selection', () => {
    const keyboard = nextDetailsFocusIntent({ token: 0, requested: false }, true);
    const pointer = nextDetailsFocusIntent(keyboard, false);
    expect(keyboard).toEqual({ token: 1, requested: true });
    expect(pointer).toEqual({ token: 2, requested: false });
  });
});
