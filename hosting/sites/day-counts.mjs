// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
/** Forecast days count once; climate days represent their share of the calendar month. */
export function countDayScores(dayScores, countBasis, dayWeights = dayScores.map(() => 1)) {
  const count = predicate => Math.round(dayScores.reduce((sum, score, index) => sum + (predicate(score) ? dayWeights[index] : 0), 0));
  return {
    niceDayCount: count(score => score >= 55),
    perfectDayCount: count(score => score >= 85),
    awfulDayCount: count(score => score < 25),
    countedDays: Math.round(dayWeights.reduce((sum, weight) => sum + weight, 0)),
    countBasis,
    dayScores,
    dayWeights,
  };
}
