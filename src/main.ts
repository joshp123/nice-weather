// Written by AI on 2026.10.17 by model Claude Fable 5.1; the page layout reshaped on 2026.10.18 by model claude opus 5.5.
// Puts the map, the engine and the interface together. The engine draws and judges; the Worker names places;
// this file holds what the visitor is looking at and keeps the link in step with it.
import './styles.css';
import { createWeatherEngine, tasteFromSliders, type Month, type Period, type PointVerdict, type WeatherEngine } from './engine';
import type { Actions } from './ui/actions';
import { fetchForecastIndex, fetchPlace, resolveSlug, type PlaceDetails, type SearchResult } from './ui/api';
import { renderCard } from './ui/card';
import { button, closeIcon, el, shareIcon, tasteIcon } from './ui/dom';
import { countryFlag } from './ui/countries';
import { requestHome, inferredHome, timeZoneHome, isHomeWinter, regionalComparison } from './ui/home';
import { createHeadline, forecastFreshness, headlineContext, headlineSentence, nicestPlaces, placeNames, viewportRegion, type AnswerLine, type HeadlinePlace } from './ui/headline';
import { createMap, type WeatherMap } from './ui/map';
import { DEFAULT_PERIOD, datedForecastPeriod, daysInPeriod, isForecast, periodTitle, samePeriod, upcomingForecastDays, type ForecastIndex } from './ui/periods';
import { createSearch } from './ui/search';
import { rankingPopulation } from './ui/ranking';
import { drawShareCard, renderPoster, type ShareContext } from './ui/share';
import { DEFAULT_SLIDERS, buildLink, loadSavedView, parseLink, saveView, slidersAreDefault, type AppState, type Place, type SliderId } from './ui/state';
import { createTastePanel } from './ui/taste';
import { bandColour, textOnBand } from './palette';
import { renderTimeBar } from './ui/timebar';

// Shared links and share cards always point at the live site, also when made from a local copy.
const PUBLIC_ORIGIN = 'https://nice.jjpcodes.com';
const dataUrl = '/data/v6';
const theme = matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
const phone = matchMedia('(max-width: 720px)');

const link = parseLink(new URL(location.href));
const saved = loadSavedView();
const restoreSavedView = location.pathname === '/' && !location.search;
let state: AppState = { period: link.period ?? (restoreSavedView ? saved.period : null) ?? DEFAULT_PERIOD, sliders: { ...DEFAULT_SLIDERS, ...(restoreSavedView ? saved.sliders : {}), ...link.sliders }, place: null };
let forecast: ForecastIndex | null = null;
let details: PlaceDetails | null = null;
let verdict: PointVerdict | null = null;
let headlineText = '';
let headlineRegion = 'the world';
let home = timeZoneHome();
let homeOverridden = false;
let rankedPlaces: HeadlinePlace[] = [];
let pickerOpen = false;
let listOpen = false;
let listCandidates: ReturnType<WeatherMap['rankingCandidates']> | null = null;
let playing: number | undefined;
let posterRequest = 0;
let tasteOpen = false;
let shareOpen = false;
let periodProblem: string | null = null;
/** A passing message beside the time: finding your location, a poster that failed. */
let barMessage: AnswerLine | null = null;
/** The camera is remembered only once the visitor has moved the map, so a first visit always frames the world. */
let cameraChosen = Boolean(saved.camera);
let placeRequest = 0;
let weatherLoading = true;
let remainingCitiesRequested = false;
let remainingCitiesReady = false;

// The page: a top bar (the brand, the answer, search and tools), the map, and a bottom bar (when, and the key).
// Panels open on the same margins: the list and the time picker on the left, a place and the taste on the right.
const app = document.getElementById('app')!;
const topBar = el('header', 'top-bar');
const topActions = el('div', 'top-actions');
const mapContainer = el('div', 'map');
const bottomBar = el('footer', 'bottom-bar');
const headline = createHeadline(() => toggleList());
const wordmark = el('h1', 'wordmark', 'Nice!');
const cardSlot = el('div', 'slot slot-card');
const tasteSlot = el('div', 'slot slot-taste');
const listSlot = el('div', 'slot slot-list');
const pickerSlot = el('div', 'slot slot-picker');
const posterSlot = el('div', 'poster-slot');
app.append(topBar, mapContainer, bottomBar, listSlot, pickerSlot, tasteSlot, cardSlot, posterSlot);

const startCamera = link.target?.kind === 'point'
  ? { center: [link.target.longitude, link.target.latitude] as const, zoom: link.target.zoom ?? 5 }
  : saved.camera;
const weatherMap: WeatherMap = await createMap(mapContainer, theme, startCamera, dataUrl, home?.longitude);
const { map } = weatherMap;
window.niceWeatherMap = map;
const engine: WeatherEngine = createWeatherEngine(map, {
  dataUrl,
  onRedraw: timing => { performance.measure('nice-weather-redraw', { start: performance.now() - timing.milliseconds, end: performance.now() }); afterRedraw(); },
  onLoadingChange(loading) {
    weatherLoading = loading;
    if (!loading) labelCities();
    if (state.place) {
      verdict = engine.judgePoint(state.place.latitude, state.place.longitude);
      weatherMap.showChosenPlace({ ...state.place, score: verdict?.meanScore });
    }
    render();
  },
});
map.addLayer(engine.layer, 'sea');

const judge = (place: Place, period: Period): PointVerdict | null => engine.judgePoint(place.latitude, place.longitude, period);

// ---------------------------------------------------------------- what the visitor can do

const actions: Actions = {
  setPeriod(period) {
    if (period.kind === 'forecast-day' && forecast) period = datedForecastPeriod(forecast, period.day);
    if (samePeriod(period, state.period)) return;
    state = { ...state, period };
    if (playing && period.kind !== 'month') stopPlaying();
    void applyPeriod();
    render();
    updateLink();
  },
  setSlider(id: SliderId, value: number) {
    state = { ...state, sliders: { ...state.sliders, [id]: value } };
    void engine.setTaste(tasteFromSliders(state.sliders)).catch(showProblem);
  },
  commitSliders() {
    render();
    updateLink();
  },
  resetSliders() {
    state = { ...state, sliders: DEFAULT_SLIDERS };
    tastePanel.update(state.sliders);
    void engine.setTaste(tasteFromSliders(state.sliders)).catch(showProblem);
    render();
    updateLink();
  },
  selectPoint(latitude, longitude) {
    selectPlace({ name: '', region: '', latitude, longitude, slug: null });
  },
  selectSearchResult(result: SearchResult) {
    cameraChosen = true;
    map.flyTo({ center: [result.longitude, result.latitude], zoom: Math.max(map.getZoom(), 5) });
    selectPlace({ name: result.name, region: result.region, latitude: result.latitude, longitude: result.longitude, slug: result.slug ?? null });
  },
  locate() {
    barMessage = { text: 'Finding your location…', warning: false }; render();
    navigator.geolocation.getCurrentPosition(position => {
      const latitude = Math.round(position.coords.latitude * 100) / 100;
      const longitude = Math.round(position.coords.longitude * 100) / 100;
      map.flyTo({ center: [longitude, latitude], zoom: Math.max(map.getZoom(), 5) });
      cameraChosen = true;
      barMessage = null;
      homeOverridden = true;
      home = { name: 'Your location', region: '', latitude, longitude, slug: null };
      void fetchPlace(latitude, longitude).then(found => { home = found; render(); });
      actions.selectPoint(latitude, longitude);
    }, () => { barMessage = { text: 'Location is not available. Search for your place instead.', warning: true }; render(); }, { timeout: 15000, enableHighAccuracy: true, maximumAge: 0 });
  },
  closePlace() {
    placeRequest++;
    state = { ...state, place: null };
    details = null;
    verdict = null;
    weatherMap.showChosenPlace(null);
    render();
    updateLink();
  },
  toggleTaste() {
    tasteOpen = !tasteOpen;
    if (tasteOpen) { pickerOpen = false; listOpen = false; void engine.prepareTaste().catch(showProblem); }
    render();
  },
  toggleShare() {
    if (shareOpen) { shareOpen = false; posterRequest++; posterSlot.replaceChildren(); render(); return; }
    void openPoster().catch(error => { barMessage = { text: error instanceof Error ? error.message : String(error), warning: true }; render(); });
  },
};

function selectPlace(place: Place): void {
  pickerOpen = false; listOpen = false; barMessage = null;
  const request = ++placeRequest;
  state = { ...state, place };
  details = null;
  verdict = engine.judgePoint(place.latitude, place.longitude);
  weatherMap.showChosenPlace({ latitude: place.latitude, longitude: place.longitude, name: place.name, score: verdict?.meanScore });
  if (phone.matches) tasteOpen = false;
  render();
  updateLink();
  keepPlaceInView(place);
  if (!isForecast(state.period)) void engine.prepareYear().then(render).catch(showProblem);
  fetchPlace(place.latitude, place.longitude).then(loaded => {
    if (request !== placeRequest) return;
    details = loaded;
    const resolvedPlace: Place = { name: loaded.name, region: loaded.region, latitude: loaded.latitude, longitude: loaded.longitude, slug: loaded.slug };
    state = { ...state, place: resolvedPlace };
    verdict = engine.judgePoint(resolvedPlace.latitude, resolvedPlace.longitude);
    weatherMap.showChosenPlace({ ...resolvedPlace, score: verdict?.meanScore });
    render();
    updateLink();
    if (resolvedPlace.latitude !== place.latitude || resolvedPlace.longitude !== place.longitude) keepPlaceInView(resolvedPlace);
  }).catch(() => {
    if (request !== placeRequest || place.name) return;
    const coordinates = `${Math.abs(place.latitude).toFixed(2)}° ${place.latitude >= 0 ? 'N' : 'S'}, ${Math.abs(place.longitude).toFixed(2)}° ${place.longitude >= 0 ? 'E' : 'W'}`;
    state = { ...state, place: { ...place, name: coordinates } };
    weatherMap.showChosenPlace({ latitude: place.latitude, longitude: place.longitude, name: coordinates });
    render();
  });
}

/** On a phone the card covers the lower map; keep the chosen spot above it. */
function keepPlaceInView(place: Place): void {
  if (!phone.matches) return;
  const ease = () => requestAnimationFrame(() => {
    const sheet = cardSlot.getBoundingClientRect();
    const top = topBar.getBoundingClientRect().bottom + 16;
    const targetY = (top + sheet.top) / 2;
    map.easeTo({ duration: 0, center: [place.longitude, place.latitude], padding: { top: 0, bottom: 0, left: 0, right: 0 }, offset: [0, targetY - window.innerHeight / 2] });
  });
  ease();
}

// ---------------------------------------------------------------- the engine

async function applyPeriod(): Promise<void> {
  const period = state.period;
  periodProblem = null;
  weatherLoading = true;
  verdict = null;
  rankedPlaces = [];
  headlineText = `Where is it nice? ${periodTitle(period, forecast)}.`;
  render();
  try {
    await engine.setPeriod(period);
    if (samePeriod(period, state.period)) {
      weatherLoading = engine.loading;
      afterRedraw();
    }
  } catch (error) {
    if (!samePeriod(period, state.period)) return;
    weatherLoading = false;
    periodProblem = `Weather data for ${periodTitle(period, forecast).toLowerCase()} is missing: ${error instanceof Error ? error.message : String(error)}`;
    render();
  }
}

function showProblem(error: unknown): void {
  periodProblem = error instanceof Error ? error.message : String(error);
  render();
}

/** After every redraw: numbers on the city labels, the headline, and the chosen place's verdict. */
function afterRedraw(): void {
  const started = performance.now();
  if (!remainingCitiesRequested) {
    remainingCitiesRequested = true;
    void weatherMap.loadRemainingCities().then(() => {
      remainingCitiesReady = true;
      labelCities();
      render();
    });
  }
  periodProblem = null;
  labelCities();
  const labelled = performance.now();
  const ranked = performance.now();
  if (state.place) {
    verdict = engine.judgePoint(state.place.latitude, state.place.longitude);
    weatherMap.showChosenPlace({ ...state.place, score: verdict?.meanScore });
  }
  render();
  performance.measure('nice-weather-interface', { start: started, end: performance.now(), detail: { labels: labelled - started, ranking: ranked - labelled, render: performance.now() - ranked } });
}

function updateHeadline(): void {
  if (!remainingCitiesReady || shareOpen) return;
  const population = rankingPopulation(map.getZoom());
  const candidates = listOpen && listCandidates ? listCandidates : weatherMap.rankingCandidates(population);
  headlineRegion = viewportRegion(map.getBounds());
  rankedPlaces = nicestPlaces(engine, candidates, map.getBounds(), 8, population);
  headlineText = !rankedPlaces.length && candidates.some(place => engine.pointLoading(place.latitude, place.longitude))
    ? 'Finding the nicest places on this map…'
    : headlineSentence(state.period, forecast, rankedPlaces.slice(0, 3), headlineRegion);
}

function labelCities(): void {
  if (weatherLoading) return;
  updateHeadline();
  const priority = new Set(rankedPlaces.slice(0, 3).map(place => place.slug));
  const cities = [...new Map([...weatherMap.labelCandidates(), ...weatherMap.visibleCities().filter(place => priority.has(place.slug))].map(place => [place.slug, place])).values()];
  const counts = engine.countPoints(cities);
  weatherMap.labelCities(cities, engine.scorePoints(cities), state.period.kind === 'forecast-day' ? engine.scorePoints(cities) : Float32Array.from(cities, (_, index) => counts[index * 3]), rankedPlaces.slice(0, 3).map(place => place.slug!));
}

function chooseRankedPlace(place: HeadlinePlace): void {
  actions.selectSearchResult({ ...place, region: place.region ?? '', slug: place.slug });
}

function stopPlaying(): void { window.clearTimeout(playing); playing = undefined; }
function togglePlaying(): void {
  if (playing) { stopPlaying(); render(); return; }
  const advance = async () => {
    const month = state.period.kind === 'month' ? (state.period.month % 12 + 1) as Month : 1;
    state = { ...state, period: { kind: 'month', month } };
    await applyPeriod(); updateLink();
    if (playing) { playing = window.setTimeout(advance, 1200); render(); }
  };
  playing = window.setTimeout(advance, 0);
  render();
}

function toggleList(): void {
  listCandidates = listOpen ? null : weatherMap.rankingCandidates(rankingPopulation(map.getZoom()));
  listOpen = !listOpen;
  pickerOpen = false;
  tasteOpen = false;
  if (state.place) actions.closePlace();
  else render();
}

function renderList(stretchLabel: string): void {
  if (!listOpen) {
    listSlot.replaceChildren();
    return;
  }
  const reference = home ? judge(home, state.period) : null;
  const days = reference?.countedDays ?? daysInPeriod(state.period, forecast);
  const close = button('icon-button', '', toggleList, 'Close');
  close.innerHTML = closeIcon;
  const title = state.period.kind === 'forecast-day' ? 'Highest scores' : 'Nicest places';
  const list = el('section', 'place-list panel', el('header', 'panel-header', el('div', '', el('h2', 'panel-title', title), el('p', 'panel-subtitle', state.period.kind === 'forecast-day' ? 'Score out of 100' : `Nice days out of ${days}`)), close));
  const rows = el('ol', 'ranked-places');
  if (!rankedPlaces.length) list.append(el('p', 'home-line', headlineText));
  rankedPlaces.slice(0, phone.matches ? 8 : 5).forEach((place, index) => {
    const count = el('span', 'count-pill', state.period.kind === 'forecast-day' ? Math.round(place.score) : Math.round(place.niceDayCount));
    count.style.background = bandColour(place.score);
    count.style.color = textOnBand(place.score);
    const row = button('ranked-place', el('span', 'rank-number', index + 1), () => chooseRankedPlace(place));
    row.append(el('span', 'rank-name', el('span', '', `${countryFlag(place)} ${place.name}`.trim()), el('small', '', `nice ${place.niceDayCount} · perfect ${place.perfectDayCount}`)), count);
    rows.append(el('li', '', row));
  });
  list.append(rows, el('p', 'home-line', 'A perfect day counts twice. Ties favour larger places.'));
  const homeVerdict = home ? judge(home, state.period) : null;
  if (home && homeVerdict) {
    const homeButton = button('inline-place', `${countryFlag(home)} ${home.name} (home)`, () => actions.selectSearchResult(home!));
    const homeLine = el('p', 'home-line', homeButton, `: awful on ${Math.round(homeVerdict.awfulDayCount)} of ${homeVerdict.countedDays ?? days} days in ${stretchLabel}.`);
    const regional = regionalComparison(home);
    const regionalVerdict = judge(regional, state.period);
    const comparison = regionalVerdict && regional.name !== home.name ? el('p', 'home-line', button('inline-place', `${countryFlag(regional)} ${regional.name}`, () => actions.selectSearchResult(regional)), `: ${Math.round(regionalVerdict.niceDayCount)} nice days over the same stretch.`) : null;
    list.append(el('div', 'home-lines', homeLine, comparison));
  }
  listSlot.replaceChildren(list);
}

async function openPoster(): Promise<void> {
  if (playing) { stopPlaying(); render(); }
  const request = ++posterRequest;
  const context = shareContext();
  const poster = await renderPoster(context, () => { shareOpen = false; posterSlot.replaceChildren(); render(); });
  if (request !== posterRequest) return;
  shareOpen = true;
  posterSlot.replaceChildren(poster);
  render();
}

// ---------------------------------------------------------------- the interface

const tastePanel = createTastePanel(state.sliders, actions);
tasteSlot.append(tastePanel.element);
const search = createSearch(actions);

function shareContext(): ShareContext {
  return { map, place: state.place, verdict, period: state.period, forecast, headline: headlineText, link: new URL(buildLink(state.place ? state : { ...state, place: { name: '', region: '', slug: null, latitude: map.getCenter().lat, longitude: map.getCenter().lng } }, map.getZoom()), PUBLIC_ORIGIN).href };
}

/** Taste and Share: words in the top bar on a computer, icons beside the time on a phone. */
function toolButton(label: string, icon: string, onClick: () => void, active: boolean): HTMLButtonElement {
  const tool = button(phone.matches ? `icon-button${active ? ' selected' : ''}` : `button${active ? ' selected' : ''}`, '', onClick, label);
  if (phone.matches) tool.innerHTML = icon;
  else tool.textContent = label;
  tool.setAttribute('aria-pressed', String(active));
  return tool;
}

function render(): void {
  const tasteIsDefault = slidersAreDefault(state.sliders);
  const status: AnswerLine | null = periodProblem ? { text: periodProblem, warning: true }
    : weatherLoading ? { text: `Loading the weather for ${periodTitle(state.period, forecast).toLowerCase()}…`, warning: false }
    : !rankedPlaces.length ? { text: headlineText, warning: false } : null;
  headline.set(headlineContext(state.period, forecast, headlineRegion), status ? null : placeNames(rankedPlaces, chooseRankedPlace), status);

  const scorePlace = state.place ?? home;
  const scoreDetails = state.place ? details : null;
  const monthScores = scorePlace ? Array.from({ length: 12 }, (_, index) => judge(scorePlace, { kind: 'month', month: (index + 1) as Month })?.meanScore ?? (tasteIsDefault ? scoreDetails?.months?.[index]?.score : undefined) ?? NaN) : undefined;
  const dayScores = scorePlace && forecast ? Array.from({ length: forecast.days }, (_, day) => judge(scorePlace, { kind: 'forecast-day', day })?.meanScore ?? NaN) : undefined;
  const scoreVerdict = scorePlace ? judge(scorePlace, state.period) : null;
  const stretchLabel = `${isHomeWinter(state.period, home) ? 'your winter, ' : ''}${isForecast(state.period) ? periodTitle(state.period, forecast).replace(/^Next/, 'the next') : state.period.kind === 'year' ? 'the whole year' : periodTitle(state.period, forecast)}`;
  const note = barMessage ?? (isForecast(state.period) && forecast ? forecastFreshness(forecast) : null);
  const bar = renderTimeBar({ period: state.period, forecast, note,
    home: home ?? undefined, monthScores, dayScores, niceDayCount: scoreVerdict?.niceDayCount, countedDays: scoreVerdict?.countedDays, countBasis: scoreVerdict?.countBasis,
    scorePlaceName: scorePlace?.name, pickerOpen, playing: Boolean(playing), stretchLabel,
    togglePicker: () => { pickerOpen = !pickerOpen; tasteOpen = false; listOpen = false; if (pickerOpen && !isForecast(state.period)) void engine.prepareYear().then(render).catch(showProblem); render(); },
    playYear: togglePlaying,
  }, actions);
  pickerSlot.replaceChildren(bar.picker);
  const taste = toolButton('Your taste', tasteIcon, actions.toggleTaste, tasteOpen);
  if (!tasteIsDefault) { taste.classList.add('changed'); taste.title = 'Your taste differs from the default'; }
  const share = toolButton('Share', shareIcon, actions.toggleShare, shareOpen);
  if (phone.matches) {
    topActions.replaceChildren(search.openButton, search.locateButton);
    topBar.replaceChildren(wordmark, topActions, search.field);
    bottomBar.replaceChildren(...[el('div', 'bar-row', bar.timeButton, taste, share), headline.element, bar.note, bar.legend, bar.smallPrint].filter(node => node !== null));
  } else {
    topActions.replaceChildren(search.field, search.locateButton, taste, share);
    topBar.replaceChildren(wordmark, headline.element, topActions);
    bottomBar.replaceChildren(el('div', 'bar-start', bar.timeButton, bar.note), el('div', 'bar-end', bar.legend, bar.smallPrint));
  }
  renderList(stretchLabel);

  if (state.place) {
    const place = state.place;
    const dayScores = state.period.kind === 'forecast' && verdict?.dayScores ? verdict.dayScores : forecast && isForecast(state.period)
      ? Array.from({ length: forecast.days }, (_, day) => judge(place, { kind: 'forecast-day', day })?.meanScore ?? null) : null;
    const monthScores = !isForecast(state.period)
      ? Array.from({ length: 12 }, (_, index) => judge(place, { kind: 'month', month: (index + 1) as 1 })?.meanScore ?? null) : null;
    const allNull = (scores: readonly (number | null)[] | null) => scores !== null && scores.every(score => score === null);
    cardSlot.replaceChildren(renderCard({
      place, details, verdict, taste: tasteFromSliders(state.sliders),
      stretchLabel: `of ${stretchLabel}`,
      homeComparison: home ? { place: home, verdict: judge(home, state.period) } : undefined,
      regionalComparison: { place: regionalComparison(home), verdict: judge(regionalComparison(home), state.period) }, period: state.period, forecast, tasteIsDefault, loading: weatherLoading || engine.pointLoading(place.latitude, place.longitude), problem: periodProblem,
      dayScores: allNull(dayScores) ? null : dayScores, monthScores: allNull(monthScores) ? null : monthScores,
    }, actions));
  } else {
    cardSlot.replaceChildren();
  }
  tastePanel.element.hidden = !tasteOpen;
  app.classList.toggle('taste-open', tasteOpen);
  app.classList.toggle('share-open', shareOpen);
  app.classList.toggle('picker-open', pickerOpen);
  app.classList.toggle('list-open', listOpen);
  app.classList.toggle('place-open', Boolean(state.place));
  document.title = headlineText;
}

function rememberView(): void {
  const centre = map.getCenter();
  saveView(state, cameraChosen ? { center: [centre.lng, centre.lat], zoom: map.getZoom() } : null);
}

function updateLink(): void {
  rememberView();
  const link = buildLink(state, map.getZoom());
  history.replaceState(null, '', link);
}

document.addEventListener('keydown', event => {
  if (event.key !== 'Escape' || (event.target instanceof HTMLInputElement && event.target.type === 'search')) return;
  if (shareOpen) { actions.toggleShare(); return; }
  else if (pickerOpen) pickerOpen = false;
  else if (listOpen) listOpen = false;
  else if (tasteOpen) tasteOpen = false;
  else if (state.place) { actions.closePlace(); return; }
  else return;
  render();
});

// The map sits between the bars; the phone's bottom bar grows with its answer, so it is measured.
let measurementFrame = 0;
const measured = new ResizeObserver(() => {
  cancelAnimationFrame(measurementFrame);
  measurementFrame = requestAnimationFrame(() => {
    document.documentElement.style.setProperty('--top-bar-height', `${topBar.offsetHeight}px`);
    document.documentElement.style.setProperty('--bottom-bar-height', `${bottomBar.offsetHeight}px`);
  });
});
measured.observe(topBar);
measured.observe(bottomBar);
phone.addEventListener('change', render);
const cardMeasured = new ResizeObserver(() => {
  if (phone.matches && state.place && cardSlot.offsetHeight > 0) keepPlaceInView(state.place);
});
cardMeasured.observe(cardSlot);

// ---------------------------------------------------------------- the map's events

map.on('click', event => {
  const city = map.queryRenderedFeatures(event.point, { layers: ['headline-labels', 'city-labels'] })[0];
  if (city?.geometry.type === 'Point') {
    const [longitude, latitude] = city.geometry.coordinates;
    actions.selectSearchResult({ name: city.properties.name, region: '', slug: city.properties.slug, latitude, longitude });
  } else actions.selectPoint(event.lngLat.lat, event.lngLat.lng);
});
for (const layer of ['headline-labels', 'city-labels']) {
  map.on('mouseenter', layer, () => { map.getCanvas().style.cursor = 'pointer'; });
  map.on('mouseleave', layer, () => { map.getCanvas().style.cursor = ''; });
}
map.on('dragstart', () => { cameraChosen = true; });
map.on('zoomstart', event => { if (event.originalEvent) cameraChosen = true; });
map.on('moveend', () => {
  listCandidates = null;
  rememberView();
  if (state.place && !state.place.slug) updateLink();
  if (!weatherLoading) {
    labelCities();
    render();
  }
});
map.on('idle', () => requestAnimationFrame(() => {
  if (weatherLoading) return;
  const previous = JSON.stringify(rankedPlaces);
  const previousRegion = headlineRegion;
  updateHeadline();
  if (previous !== JSON.stringify(rankedPlaces) || previousRegion !== headlineRegion) { labelCities(); render(); }
}));
map.on('zoomend', () => { if (!weatherLoading) labelCities(); });
void weatherMap.citiesReady.then(() => {
  labelCities();
  if (!weatherLoading) render();
});

// ---------------------------------------------------------------- start

window.niceWeatherShareCard = (width: number, height: number) => drawShareCard(width, height, shareContext());

void requestHome().then(async found => {
  const inferred = found ?? await inferredHome();
  if (homeOverridden) return;
  home = inferred;
  render();
}).catch(() => {});

forecast = await fetchForecastIndex(dataUrl).catch(() => null);
if (state.period.kind === 'forecast-day' && forecast) {
  const day = state.period.date ? Math.round((Date.parse(`${state.period.date}T12:00:00Z`) - Date.parse(`${forecast.firstDate}T12:00:00Z`)) / 86_400_000) : state.period.day;
  if (upcomingForecastDays(forecast).includes(day)) state = { ...state, period: datedForecastPeriod(forecast, day) };
  else state = { ...state, period: { kind: 'forecast' } };
}
render();
void engine.setTaste(tasteFromSliders(state.sliders)).catch(showProblem);
void applyPeriod();
if (state.period.kind === 'forecast-day') updateLink();

if (link.target?.kind === 'point') {
  actions.selectPoint(link.target.latitude, link.target.longitude);
} else if (restoreSavedView && saved.place) {
  selectPlace(saved.place);
} else if (link.target?.kind === 'slug') {
  const place = await resolveSlug(link.target.slug).catch(() => null);
  if (place) {
    map.jumpTo({ center: [place.longitude, place.latitude], zoom: Math.max(map.getZoom(), 5) });
    selectPlace(place);
  } else {
    barMessage = { text: 'Place not found. Search for it, or choose it on the map.', warning: true };
    render();
  }
}

declare global {
  interface Window { niceWeatherShareCard: (width: number, height: number) => Promise<Blob>; niceWeatherMap: unknown }
}
