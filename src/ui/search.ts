// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// Find a place by name. Enter takes the first result; the arrow uses the device's location.
import type { Actions } from './actions';
import { searchPlaces } from './api';
import { button, el, locateIcon, searchIcon } from './dom';

export function createSearch(actions: Actions): HTMLElement {
  const input = el('input', 'search-input');
  input.type = 'search';
  input.placeholder = 'Search a place';
  input.autocomplete = 'off';
  input.setAttribute('aria-label', 'Search a place');
  const results = el('ul', 'search-results');
  let latest = 0;
  let timer: number | undefined;
  const clear = () => results.replaceChildren();
  input.oninput = () => {
    window.clearTimeout(timer);
    const query = input.value.trim();
    if (query.length < 2) { clear(); return; }
    timer = window.setTimeout(async () => {
      const request = ++latest;
      const found = await searchPlaces(query).catch(() => []);
      if (request !== latest) return;
      results.replaceChildren(...found.slice(0, 7).map(result => {
        const pick = button('', '', () => { input.value = result.name; clear(); input.blur(); actions.selectSearchResult(result); });
        pick.append(el('span', '', result.name), el('span', 'muted', result.region));
        return el('li', '', pick);
      }));
    }, 120);
  };
  input.onkeydown = event => {
    if (event.key === 'Enter') results.querySelector('button')?.click();
    if (event.key === 'Escape') { clear(); input.blur(); }
  };
  input.onblur = () => window.setTimeout(clear, 150);
  const icon = el('span', 'search-icon');
  icon.innerHTML = searchIcon;
  const locate = button('icon-button', '', actions.locate, 'Use my location');
  locate.innerHTML = locateIcon;
  return el('div', 'search panel', icon, input, locate, results);
}
