// Written by AI on 2026.10.17 by model GPT-6.1 Sol.
import { createScoreCurve, type ScoreAxis } from './ui/scoreCurve';
for (const target of document.querySelectorAll<HTMLElement>('[data-score-axis]')) {
  target.append(createScoreCurve(target.dataset.scoreAxis as ScoreAxis).element);
}
