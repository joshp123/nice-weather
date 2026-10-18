// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
/** Count the actual daily scores, after spatial interpolation, rather than an area share. */
export function countDayScores(dayScores, countBasis) {
  return {
    niceDayCount: dayScores.filter(score => score >= 55).length,
    perfectDayCount: dayScores.filter(score => score >= 85).length,
    awfulDayCount: dayScores.filter(score => score < 25).length,
    countedDays: dayScores.length,
    countBasis,
    dayScores,
  };
}
