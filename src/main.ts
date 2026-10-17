// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// Puts the map, the engine and the interface together. The engine draws and judges; the Worker names places;
// this file holds what the visitor is looking at and keeps the link in step with it.
import './styles.css';
import { createWeatherEngine, tasteFromSliders, type Period, type PointVerdict, type WeatherEngine } from './engine';
import type { Actions } from './ui/actions';
import { fetchForecastIndex, fetchPlace, resolveSlug, type PlaceDetails, type SearchResult } from './ui/api';
import { renderCard } from './ui/card';
import { el } from './ui/dom';
import { createHeadline, forecastFreshness, headlineSentence, nicestPlaces } from './ui/headline';
import { createMap, type WeatherMap } from './ui/map';
import { DEFAULT_PERIOD, isForecast, periodTitle, samePeriod, upcomingForecastDays, type ForecastIndex } from './ui/periods';
import { createSearch } from './ui/search';
import { drawShareCard, renderShareMenu, type ShareContext } from './ui/share';
import { DEFAULT_SLIDERS, buildLink, loadSavedView, parseLink, saveView, slidersAreDefault, type AppState, type Place, type SliderId } from './ui/state';
import { createTastePanel } from './ui/taste';
import { renderTimeBar } from './ui/timebar';

// Shared links and share cards always point at the live site, also when made from a local copy.
const PUBLIC_ORIGIN = 'https://nice.jjpcodes.com';
const parameters = new URLSearchParams(location.search);
const dataUrl = parameters.get('data') ?? '/data/v6';
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
let tasteOpen = false;
let shareOpen = false;
let periodProblem: string | null = null;
let placeRequest = 0;
let weatherLoading = true;
let remainingCitiesRequested = false;

const app = document.getElementById('app')!;
const mapContainer = el('div', 'map');
const headline = createHeadline();
const searchSlot = el('div', 'slot slot-search');
const cardSlot = el('div', 'slot slot-card');
const tasteSlot = el('div', 'slot slot-taste');
const barSlot = el('div', 'slot slot-bar');
app.append(mapContainer, el('div', 'slot slot-headline', headline.element), searchSlot, tasteSlot, cardSlot, barSlot);

const startCamera = link.target?.kind === 'point'
  ? { center: [link.target.longitude, link.target.latitude] as const, zoom: link.target.zoom ?? 5 }
  : saved.camera;
const weatherMap: WeatherMap = await createMap(mapContainer, theme, startCamera, dataUrl);
const { map } = weatherMap;
const engine: WeatherEngine = createWeatherEngine(map, {
  dataUrl,
  onRedraw: () => afterRedraw(),
  onLoadingChange(loading) {
    weatherLoading = loading;
    labelCities();
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
    if (samePeriod(period, state.period)) return;
    state = { ...state, period };
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
    map.flyTo({ center: [result.longitude, result.latitude], zoom: Math.max(map.getZoom(), 5) });
    selectPlace({ name: result.name, region: result.region, latitude: result.latitude, longitude: result.longitude, slug: result.slug ?? null });
  },
  locate() {
    headline.set(headlineText, { text: 'Finding your location… Your position is not stored.', warning: false });
    navigator.geolocation.getCurrentPosition(position => {
      const { latitude, longitude } = position.coords;
      map.flyTo({ center: [longitude, latitude], zoom: Math.max(map.getZoom(), 5) });
      actions.selectPoint(latitude, longitude);
    }, () => headline.set(headlineText, { text: 'Location is not available. Search for your place instead.', warning: true }), { timeout: 15000 });
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
    if (tasteOpen) shareOpen = false;
    render();
  },
  toggleShare() {
    shareOpen = !shareOpen;
    if (shareOpen && phone.matches) tasteOpen = false;
    render();
  },
};

function selectPlace(place: Place): void {
  const request = ++placeRequest;
  state = { ...state, place };
  details = null;
  verdict = engine.judgePoint(place.latitude, place.longitude);
  weatherMap.showChosenPlace({ latitude: place.latitude, longitude: place.longitude, name: place.name, score: verdict?.meanScore });
  if (phone.matches) tasteOpen = false;
  render();
  updateLink();
  keepPlaceInView(place);
  fetchPlace(place.latitude, place.longitude).then(loaded => {
    if (request !== placeRequest) return;
    details = loaded;
    state = { ...state, place: { ...place, name: loaded.name, region: loaded.region, slug: loaded.slug ?? place.slug } };
    weatherMap.showChosenPlace({ latitude: place.latitude, longitude: place.longitude, name: loaded.name, score: verdict?.meanScore });
    render();
    updateLink();
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
    const top = headline.element.getBoundingClientRect().bottom + 16;
    const targetY = (top + sheet.top) / 2;
    map.easeTo({ center: [place.longitude, place.latitude], padding: { top: 0, bottom: 0, left: 0, right: 0 }, offset: [0, targetY - window.innerHeight / 2] });
  });
  ease();
}

// ---------------------------------------------------------------- the engine

async function applyPeriod(): Promise<void> {
  const period = state.period;
  periodProblem = null;
  weatherLoading = true;
  verdict = null;
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
  if (!remainingCitiesRequested) {
    remainingCitiesRequested = true;
    void weatherMap.loadRemainingCities().then(labelCities);
  }
  periodProblem = null;
  labelCities();
  headlineText = headlineSentence(state.period, forecast, nicestPlaces(engine)) || `Where is it nice? ${periodTitle(state.period, forecast)}.`;
  if (state.place) {
    verdict = engine.judgePoint(state.place.latitude, state.place.longitude);
    weatherMap.showChosenPlace({ ...state.place, score: verdict?.meanScore });
  }
  render();
}

function labelCities(): void {
  const cities = weatherMap.visibleCities();
  weatherMap.labelCities(cities, engine.scorePoints(cities));
}

// ---------------------------------------------------------------- the interface

const tastePanel = createTastePanel(state.sliders, actions);
tasteSlot.append(tastePanel.element);
searchSlot.append(createSearch(actions));

function shareContext(): ShareContext {
  return { map, place: state.place, verdict, period: state.period, forecast, headline: headlineText, link: new URL(buildLink(state.place ? state : { ...state, place: { name: '', region: '', slug: null, latitude: map.getCenter().lat, longitude: map.getCenter().lng } }, map.getZoom()), PUBLIC_ORIGIN).href };
}

function render(): void {
  const tasteIsDefault = slidersAreDefault(state.sliders);
  const note = periodProblem ? { text: periodProblem, warning: true } : weatherLoading ? { text: `Loading weather figures for ${periodTitle(state.period, forecast).toLowerCase()}…`, warning: false } : isForecast(state.period) && forecast ? forecastFreshness(forecast) : null;
  headline.set(headlineText, note);

  const bar = renderTimeBar({ period: state.period, forecast, tasteOpen, tasteIsDefault, shareOpen, theme }, actions);
  if (shareOpen) bar.append(renderShareMenu(shareContext));
  barSlot.replaceChildren(bar);

  if (state.place) {
    const place = state.place;
    const dayScores = forecast && isForecast(state.period)
      ? Array.from({ length: forecast.days }, (_, day) => judge(place, { kind: 'forecast-day', day })?.meanScore ?? null) : null;
    const monthScores = !isForecast(state.period)
      ? Array.from({ length: 12 }, (_, index) => judge(place, { kind: 'month', month: (index + 1) as 1 })?.meanScore ?? null) : null;
    const allNull = (scores: readonly (number | null)[] | null) => scores !== null && scores.every(score => score === null);
    cardSlot.replaceChildren(renderCard({
      place, details, verdict, period: state.period, forecast, tasteIsDefault, loading: weatherLoading, problem: periodProblem,
      dayScores: allNull(dayScores) ? null : dayScores, monthScores: allNull(monthScores) ? null : monthScores,
    }, actions));
  } else {
    cardSlot.replaceChildren();
  }
  tastePanel.element.hidden = !tasteOpen;
  app.classList.toggle('taste-open', tasteOpen);
  app.classList.toggle('share-open', shareOpen);
  app.classList.toggle('has-place', Boolean(state.place));
  document.title = state.place?.name ? `${state.place.name} ${isForecast(state.period) ? '' : `in ${periodTitle(state.period, forecast)} `}· Nice Weather` : 'Nice Weather';
}

function rememberView(): void {
  const centre = map.getCenter();
  saveView(state, { center: [centre.lng, centre.lat], zoom: map.getZoom() });
}

function updateLink(): void {
  rememberView();
  const link = buildLink(state, map.getZoom());
  const debugData = parameters.get('data');
  history.replaceState(null, '', debugData ? `${link}${link.includes('?') ? '&' : '?'}data=${encodeURIComponent(debugData)}` : link);
}

document.addEventListener('keydown', event => {
  if (event.key !== 'Escape' || (event.target instanceof HTMLInputElement && event.target.type === 'search')) return;
  if (shareOpen) shareOpen = false;
  else if (tasteOpen) tasteOpen = false;
  else if (state.place) { actions.closePlace(); return; }
  else return;
  render();
});

// The panels stack under the headline and the search box; the card must never sit under the bar.
const measured = new ResizeObserver(() => {
  app.style.setProperty('--search-height', `${searchSlot.offsetHeight}px`);
  app.style.setProperty('--headline-height', `${headline.element.offsetHeight}px`);
  app.style.setProperty('--bar-height', `${barSlot.offsetHeight}px`);
  document.documentElement.style.setProperty('--bar-height', `${barSlot.getBoundingClientRect().height}px`);
  if (state.place && phone.matches && !tasteOpen && !shareOpen) keepPlaceInView(state.place);
});
measured.observe(headline.element);
measured.observe(searchSlot);
measured.observe(barSlot);
measured.observe(cardSlot);

// ---------------------------------------------------------------- the map's events

map.on('click', event => {
  const city = map.queryRenderedFeatures(event.point, { layers: ['city-labels'] })[0];
  if (city?.geometry.type === 'Point') {
    const [longitude, latitude] = city.geometry.coordinates;
    actions.selectSearchResult({ name: city.properties.name, region: '', slug: city.properties.slug, latitude, longitude });
  } else actions.selectPoint(event.lngLat.lat, event.lngLat.lng);
});
map.on('mouseenter', 'city-labels', () => { map.getCanvas().style.cursor = 'pointer'; });
map.on('mouseleave', 'city-labels', () => { map.getCanvas().style.cursor = ''; });
map.on('moveend', () => {
  rememberView();
  if (state.place && !state.place.slug) updateLink();
});
void weatherMap.citiesReady.then(labelCities);

// ---------------------------------------------------------------- start

window.niceWeatherShareCard = (width: number, height: number) => drawShareCard(width, height, shareContext());

forecast = await fetchForecastIndex(dataUrl).catch(() => null);
if (state.period.kind === 'forecast-day' && !(forecast && upcomingForecastDays(forecast).includes(state.period.day))) state = { ...state, period: { kind: 'forecast' } };
render();
void engine.setTaste(tasteFromSliders(state.sliders)).catch(showProblem);
void applyPeriod();

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
    updateLink();
  }
}

declare global {
  interface Window { niceWeatherShareCard: (width: number, height: number) => Promise<Blob> }
}
