// Written by AI (GPT-6.1 Sol), 2026-10-19.
import awfulMaximum from './awful-maximum.json' with {type:'json'};

export const AWFUL_MAXIMUM = awfulMaximum;
export const SCORE_BANDS = [
  {name:'Awful',minimum:0},
  {name:'Poor',minimum:AWFUL_MAXIMUM},
  {name:'Fair',minimum:40},
  {name:'Nice',minimum:55},
  {name:'Beautiful',minimum:70},
  {name:'Perfect',minimum:85},
];
export const SCORE_THRESHOLDS = [...SCORE_BANDS.map(band=>band.minimum),100];
export function bandOf(score) {
  return (SCORE_BANDS.findLast(band=>score>=band.minimum)??SCORE_BANDS[0]).name;
}
