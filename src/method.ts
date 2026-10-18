// Written by AI on 2026.10.17 by model GPT-6.1 Sol.
import '@fontsource/barlow/400.css';
import '@fontsource/barlow/600.css';
import './design-tokens.css';
import { createScoreCurve, type ScoreAxis } from './ui/scoreCurve';
for (const target of document.querySelectorAll<HTMLElement>('[data-score-axis]')) {
  target.append(createScoreCurve(target.dataset.scoreAxis as ScoreAxis).element);
}
