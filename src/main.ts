// Written by AI on 2026.10.16 by model claude opus 5.5.
import './styles.css';
import maplibregl from 'maplibre-gl';
import { fetchClimateIndex, fetchForecastIndex, fetchPlace, searchPlaces, type ClimateIndex, type ForecastIndex, type PlaceWeather } from './api';
import { createWeatherMap } from './map';
import { bands } from './palette';
import { layerName, loadState, saveState, type AppState } from './state';
import { element, forecastStatus, renderCard, renderDock, renderHelp, renderSearch, type Actions } from './ui';

let state: AppState = loadState();
let forecast: ForecastIndex | null = null;
let climate: ClimateIndex | null = null;
let place: PlaceWeather | null = null;
let showingHelp = false;

const app = document.getElementById('app')!;
const mapContainer = element('div', 'map');
const status = element('p', 'status');
const masthead = element('header', 'masthead panel', element('h1', '', 'Nice Weather Finder'), status);
const dockSlot = element('div', 'dock-slot');
const cardSlot = element('div', 'card-slot');
const placeMarkerElement = element('div', 'place-marker');
const placeMarker = new maplibregl.Marker({ element: placeMarkerElement });
app.append(mapContainer, masthead, element('div', 'overlay', cardSlot, dockSlot));

const { map, weather } = await createWeatherMap(mapContainer, bands, { center: state.center, zoom: state.zoom });

function update(change: Partial<AppState>): void {
  state = { ...state, ...change };
  saveState(state);
  render();
}

const actions: Actions = {
  setView: (view) => update({ view }),
  setDay: (day) => update({ day }),
  setMonth: (month) => update({ month }),
  setProfile: (profile) => update({ profile }),
  setLens: (lens) => update({ lens }),
  closePlace: () => { place = null; showingHelp = false; placeMarker.remove(); update({ place: null }); },
  choosePlace: (latitude, longitude) => selectPlace(latitude, longitude),
  showHelp: () => { showingHelp = true; render(); },
};

async function selectPlace(latitude: number, longitude: number): Promise<void> {
  showingHelp = false;
  update({ place: { latitude, longitude } });
  placeMarker.setLngLat([longitude, latitude]).addTo(map);
  const requested = state.place;
  const loaded = await fetchPlace(latitude, longitude);
  if (state.place !== requested) return;
  place = loaded;
  render();
  // On a phone the card covers the lower map; keep the chosen spot in view above it.
  if (window.matchMedia('(max-width: 720px)').matches) {
    const overlay = document.querySelector<HTMLElement>('.overlay')!;
    map.easeTo({ center: [longitude, latitude], padding: { top: 72, bottom: overlay.offsetHeight + 16, left: 0, right: 0 } });
  }
}

function showWeatherLayer(): void {
  const layer = layerName(state.profile, state.lens);
  if (state.view === 'forecast' && forecast) {
    if (state.day === 'fortnight') {
      void weather.show(`/data/forecast/${forecast.runId}/fortnight/${state.profile}.png`, forecast.grid, 100 / forecast.dates.length);
    } else {
      void weather.show(`/data/forecast/${forecast.runId}/${state.day}/${layer}.png`, forecast.grid, 1);
    }
  } else if (state.view === 'year' && climate) {
    void weather.show(`/data/climate/month-${String(state.month).padStart(2, '0')}/${layer}.png`, climate.grid, 1);
  }
}

function render(): void {
  if (forecast && state.day !== 'fortnight' && !forecast.dates.includes(state.day)) state = { ...state, day: 'fortnight' };
  if (state.view === 'forecast' && forecast) {
    const { text, stale } = forecastStatus(forecast);
    status.textContent = text;
    status.classList.toggle('stale', stale);
  } else if (climate) {
    status.textContent = `A typical year, from real days ${climate.years}`;
    status.classList.remove('stale');
  }
  const chipsScroll = dockSlot.querySelector('.chips')?.scrollLeft ?? 0;
  dockSlot.replaceChildren(renderDock(state, forecast, actions, place));
  const chips = dockSlot.querySelector<HTMLElement>('.chips');
  const selectedChip = chips?.querySelector<HTMLElement>('.chip.selected');
  if (chips && selectedChip) {
    chips.scrollLeft = chipsScroll;
    const outside = selectedChip.offsetLeft < chips.scrollLeft || selectedChip.offsetLeft + selectedChip.offsetWidth > chips.scrollLeft + chips.clientWidth;
    if (outside) chips.scrollLeft = selectedChip.offsetLeft - chips.clientWidth / 2 + selectedChip.offsetWidth / 2;
  }
  cardSlot.replaceChildren(showingHelp ? renderHelp(actions) : place ? renderCard(state, place, climate, actions) : '');
  showWeatherLayer();
}

app.append(renderSearch(
  (result) => { map.flyTo({ center: [result.longitude, result.latitude], zoom: Math.max(map.getZoom(), 6) }); void selectPlace(result.latitude, result.longitude); },
  () => navigator.geolocation.getCurrentPosition((position) => {
    const { latitude, longitude } = position.coords;
    map.flyTo({ center: [longitude, latitude], zoom: Math.max(map.getZoom(), 6) });
    void selectPlace(latitude, longitude);
  }),
  searchPlaces,
));

map.on('click', (event) => void selectPlace(event.lngLat.lat, event.lngLat.lng));
map.on('moveend', () => {
  const center = map.getCenter();
  state = { ...state, center: [center.lng, center.lat], zoom: map.getZoom() };
  saveState(state);
});

[forecast, climate] = await Promise.all([fetchForecastIndex().catch(() => null), fetchClimateIndex().catch(() => null)]);
render();
if (state.place) void selectPlace(state.place.latitude, state.place.longitude);
