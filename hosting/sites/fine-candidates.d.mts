// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
export function finePlaceCandidates(points: readonly {latitude:number;longitude:number;population?:number}[],
  overviewScores:ArrayLike<number>, view:{west:number;east:number;south:number;north:number;minimumLabelPopulation:number}):Set<number>;
