// Written by AI on 2026.10.17 by model Claude Fable 5.1; reshaped on 2026.10.18 by model claude opus 5.5.
// Find a place by name, or by the device's location. Enter takes the first result.
// On a phone the field hides behind a search button in the top bar and opens across it.
import type { Actions } from './actions';
import { searchPlaces } from './api';
import { button, closeIcon, el, locateIcon, searchIcon } from './dom';

export type SearchControls = Readonly<{ field: HTMLElement; openButton: HTMLButtonElement; locateButton: HTMLButtonElement }>;

export function createSearch(actions: Actions): SearchControls {
  const app = () => document.getElementById('app');
  const input = el('input', 'search-input');
  input.type = 'search';
  input.placeholder = 'Search a place';
  input.autocomplete = 'off';
  input.setAttribute('aria-label', 'Search a place');
  const results = el('ul', 'search-results panel');
  let latest = 0;
  let timer: number | undefined;
  const clear = () => results.replaceChildren();
  // Closing also drops a search still on its way, so late results never reopen the list.
  const close = () => { latest++; window.clearTimeout(timer); clear(); input.blur(); app()?.classList.remove('search-open'); };
  input.oninput = () => {
    latest++;
    window.clearTimeout(timer);
    const query = input.value.trim();
    if (query.length < 2) { clear(); return; }
    timer = window.setTimeout(async () => {
      const request = ++latest;
      const found = await searchPlaces(query).catch(() => []);
      if (request !== latest) return;
      results.replaceChildren(...found.slice(0, 7).map(result => {
        const pick = button('search-result', '', () => { input.value = result.name; close(); actions.selectSearchResult(result); });
        pick.append(el('span', '', result.name), el('span', 'muted', result.region));
        return el('li', '', pick);
      }));
    }, 120);
  };
  input.onkeydown = event => {
    if (event.key === 'Enter') results.querySelector('button')?.click();
    if (event.key === 'Escape') close();
  };
  input.onblur = () => window.setTimeout(clear, 150);
  const icon = el('span', 'search-icon');
  icon.innerHTML = searchIcon;
  const closeButton = button('icon-button search-close', '', close, 'Close search');
  closeButton.innerHTML = closeIcon;
  const field = el('div', 'search', icon, input, closeButton, results);

  const openButton = button('icon-button search-open-button', '', () => { app()?.classList.add('search-open'); input.focus(); }, 'Search a place');
  openButton.innerHTML = searchIcon;

  const locateButton = button('icon-button', '', () => { close(); actions.locate(); }, 'My location');
  locateButton.innerHTML = locateIcon;
  locateButton.title = 'My location. Your position finds the place name; it is not stored.';
  return { field, openButton, locateButton };
}
