// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// Six sliders in real units. Each moves one curve of the rule; the map redraws as you drag.
import { SLIDERS } from '../engine';
import type { Actions } from './actions';
import { button, closeIcon, el } from './dom';
import type { SliderId, SliderValues } from './state';

type Copy = Readonly<{ name: string; readout: (value: number) => string; hint: string }>;

const degrees = (value: number) => `${value} °C`;

const COPY: Record<SliderId, Copy> = {
  perfectFrom: { name: 'Warm enough', readout: value => `from ${degrees(value)}`, hint: 'Feels-like in the sun. Hours this warm count in full; 6 °C cooler counts 40%.' },
  tooHotFrom: { name: 'Too hot', readout: value => `from ${degrees(value)}`, hint: 'Feels-like in the shade. Hotter hours count less.' },
  walkHome: { name: 'Walk home at 21:00', readout: value => `fine from ${degrees(value)}`, hint: 'Feels-like at 21:00. Counts like two hours of the day.' },
  humidity: { name: 'Humidity', readout: value => `halves a day at ${degrees(value)} dew point`, hint: 'Dew point: 16 °C is dry, 20 muggy, 24 oppressive.' },
  wind: { name: 'Wind', readout: value => `halves a day at ${value} km/h`, hint: 'The day’s average; gusts run higher.' },
  rain: { name: 'Rain', readout: value => `halves a day at ${value} mm`, hint: 'Total from 10:00 to 21:00. 1 mm is a shower, 10 mm a wet afternoon.' },
};

export type TastePanel = Readonly<{ element: HTMLElement; update(values: SliderValues): void }>;

export function createTastePanel(values: SliderValues, actions: Actions): TastePanel {
  const inputs = new Map<SliderId, { input: HTMLInputElement; readout: HTMLElement }>();
  const close = button('icon-button', '', actions.toggleTaste, 'Close');
  close.innerHTML = closeIcon;
  const list = el('div', 'sliders');
  for (const slider of SLIDERS) {
    const copy = COPY[slider.id];
    const readout = el('output', 'slider-readout', copy.readout(values[slider.id]));
    const input = el('input', 'slider-input');
    input.type = 'range';
    input.min = String(slider.min);
    input.max = String(slider.max);
    input.step = String(slider.step);
    input.value = String(values[slider.id]);
    input.id = `slider-${slider.id}`;
    input.setAttribute('aria-label', `${copy.name}, ${slider.unit}`);
    input.oninput = () => {
      readout.textContent = copy.readout(Number(input.value));
      actions.setSlider(slider.id, Number(input.value));
    };
    input.onchange = actions.commitSliders;
    const label = el('label', 'slider-name', copy.name);
    label.htmlFor = input.id;
    list.append(el('div', 'slider', el('div', 'slider-head', label, readout), input, el('p', 'slider-hint', copy.hint)));
    inputs.set(slider.id, { input, readout });
  }
  const reset = button('text-button', 'Reset to the default', actions.resetSliders);
  const element = el('section', 'taste panel',
    el('header', 'card-header', el('div', '', el('h2', '', 'Your taste'), el('p', 'muted', 'The default is how Josh likes it: warm, sunny, dry and calm. Drag a slider and the whole map redraws.')), close),
    list,
    el('div', 'taste-foot', reset));
  return {
    element,
    update(next) {
      for (const slider of SLIDERS) {
        const entry = inputs.get(slider.id)!;
        if (Number(entry.input.value) !== next[slider.id]) entry.input.value = String(next[slider.id]);
        entry.readout.textContent = COPY[slider.id].readout(next[slider.id]);
      }
    },
  };
}
