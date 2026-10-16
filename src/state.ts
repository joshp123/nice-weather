// Written by AI on 2026.10.16 by model claude opus 5.5.
// What the person is looking at. Saved on this device so the app reopens where they left it.

export type View = 'forecast' | 'year';
export type Profile = 'nice' | 'cycling';
export type Lens = 'overall' | 'comfort' | 'dry' | 'clear' | 'calm';
export type Place = Readonly<{ latitude: number; longitude: number }>;

export type AppState = Readonly<{
  view: View;
  /** 'fortnight' or an ISO date such as 2026-10-18. */
  day: string;
  /** 1-12. */
  month: number;
  profile: Profile;
  lens: Lens;
  place: Place | null;
  center: [number, number];
  zoom: number;
}>;

const storageKey = 'nice-weather-view-v2';

const defaultState: AppState = {
  view: 'forecast', day: 'fortnight', month: new Date().getMonth() + 1, profile: 'nice', lens: 'overall', place: null,
  center: [12, 42], zoom: 2.6,
};

export function loadState(): AppState {
  try {
    return { ...defaultState, ...JSON.parse(localStorage.getItem(storageKey) ?? '{}') };
  } catch {
    return defaultState;
  }
}

export function saveState(state: AppState): void {
  localStorage.setItem(storageKey, JSON.stringify(state));
}

/** The PNG layer name for a profile and lens. Comfort, dry and clear sky do not depend on the profile. */
export function layerName(profile: Profile, lens: Lens): string {
  if (lens === 'overall') return profile;
  if (lens === 'calm') return `calm-${profile}`;
  return lens;
}
