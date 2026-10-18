// Written by AI on 2026.10.17 by model Claude Fable 5.1.
// Six sliders in real units. Each moves one curve of the rule; the map redraws as you drag.
import { SLIDERS, tasteFromSliders } from '../engine';
import { createScoreCurve, type ScoreAxis } from './scoreCurve';
import type { Actions } from './actions';
import { button, closeIcon, el } from './dom';
import type { SliderId, SliderValues } from './state';

type Copy = Readonly<{ name: string; readout: (value: number) => string; hint: string }>;

const degrees = (value: number) => `${value} °C`;

const COPY: Record<SliderId, Copy> = {
  perfectFrom: { name: 'Warm enough', readout: value => `from ${degrees(value)}`, hint: 'Feels-like in the sun. Hours this warm count in full; 6 °C cooler counts 40%.' },
  tooHotFrom: { name: 'Too hot', readout: value => `from ${degrees(value)}`, hint: 'Feels-like in the shade. Hotter hours count less.' },
  walkHome: { name: 'Walk home at 21:00', readout: value => `fine from ${degrees(value)}`, hint: 'Counts like two daytime hours. Too much heat also lowers its grade.' },
  humidity: { name: 'Humidity', readout: value => `keeps about half at ${degrees(value)} dew point`, hint: 'Dew point measures moisture in the air. Higher values feel muggier.' },
  wind: { name: 'Wind', readout: value => `halves a day at ${value} km/h`, hint: 'Mean from 10:00 to 21:00; gusts run higher.' },
  rain: { name: 'Rain', readout: value => `halves a day at ${value} mm`, hint: 'Total from 10:00 to 21:00. Rain keeps a smaller share of the day.' },
};

export type TastePanel = Readonly<{ element: HTMLElement; update(values: SliderValues): void }>;

export function createTastePanel(values: SliderValues, actions: Actions): TastePanel {
  const inputs = new Map<SliderId, { input: HTMLInputElement; readout: HTMLElement; chart: ReturnType<typeof createScoreCurve> }>();
  const axisBySlider: Record<SliderId, ScoreAxis> = { perfectFrom: 'warmth', tooHotFrom: 'heat', walkHome: 'evening', humidity: 'humidity', wind: 'wind', rain: 'rain' };
  let currentValues = values;
  const close = button('icon-button', '', actions.toggleTaste, 'Close');
  close.innerHTML = closeIcon;
  const list = el('div', 'sliders');
  for (const slider of SLIDERS) {
    const copy = COPY[slider.id];
    const chart = createScoreCurve(axisBySlider[slider.id], tasteFromSliders(values), true);
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
      currentValues = { ...currentValues, [slider.id]: Number(input.value) };
      const taste = tasteFromSliders(currentValues);
      for (const entry of inputs.values()) entry.chart.update(taste);
      actions.setSlider(slider.id, Number(input.value));
    };
    input.onchange = actions.commitSliders;
    const label = el('label', 'slider-name', copy.name);
    label.htmlFor = input.id;
    list.append(el('div', 'slider', el('div', 'slider-head', label, readout), input, chart.element, el('p', 'slider-hint', copy.hint)));
    inputs.set(slider.id, { input, readout, chart });
  }
  const reset = button('text-button', 'Reset to the default', actions.resetSliders);
  const element = el('section', 'taste panel',
    el('header', 'panel-header', el('div', '', el('h2', 'panel-title', 'Your taste'), el('p', 'panel-subtitle', 'Warm, sunny, dry and calm by default. Drag a slider and the map redraws.')), close),
    list,
    el('div', 'taste-foot', reset));
  return {
    element,
    update(next) {
      currentValues = next;
      const taste = tasteFromSliders(next);
      for (const slider of SLIDERS) {
        const entry = inputs.get(slider.id)!;
        if (Number(entry.input.value) !== next[slider.id]) entry.input.value = String(next[slider.id]);
        entry.readout.textContent = COPY[slider.id].readout(next[slider.id]);
        entry.chart.update(taste);
      }
    },
  };
}
