// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// Shared words and figures for the place card and its text API.
export function humidityWord(dewPointC) {
  if (dewPointC <= 13) return 'dry';
  if (dewPointC <= 16) return 'comfortable';
  if (dewPointC <= 18) return 'humid';
  if (dewPointC <= 21) return 'muggy';
  if (dewPointC <= 24) return 'oppressive';
  return 'miserable';
}

function afternoonWords(temperatureC) {
  if (temperatureC >= 31) return 'Hot';
  if (temperatureC >= 23) return 'Warm';
  if (temperatureC >= 19) return 'Mild';
  if (temperatureC >= 15) return 'Cool';
  if (temperatureC >= 9) return 'Chilly';
  return 'Cold';
}

function eveningWords(temperatureC) {
  if (temperatureC >= 26) return 'Hot evening';
  if (temperatureC >= 19) return 'Warm evening';
  if (temperatureC >= 16) return 'Comfortable in a t-shirt';
  if (temperatureC >= 12) return 'Cool evening';
  if (temperatureC >= 8) return 'Chilly, bring a layer';
  return 'Cold';
}

export function placeReasons(typical, singleDay = false) {
  const reason = (key, label, figure, unit, value, detail) => ({key, label, figure, unit, value, detail});
  const reasons = [reason('afternoonSun', 'Afternoon in the sun', typical.feelsInSunAt14C, '°C', `${Math.round(typical.feelsInSunAt14C)} °C feels-like`, afternoonWords(typical.feelsInSunAt14C))];
  if (typical.feelsInShadeAt15C !== undefined) reasons.push(reason('afternoonShade', 'Afternoon in the shade', typical.feelsInShadeAt15C, '°C', `${Math.round(typical.feelsInShadeAt15C)} °C feels-like at 15:00`, afternoonWords(typical.feelsInShadeAt15C)));
  reasons.push(
    reason('walkHome', 'Walk home at 21:00', typical.feelsInShadeAt21C, '°C', `${Math.round(typical.feelsInShadeAt21C)} °C in the shade`, eveningWords(typical.feelsInShadeAt21C)),
    reason('rain', 'Rain, 10:00 to 21:00', typical.rainMmPerDay, 'mm', `${typical.rainMmPerDay.toFixed(1)} mm${singleDay ? '' : ' a day'}`, `${typical.rainMmPerDay < 0.5 ? 'Mostly dry' : typical.rainMmPerDay < 2 ? 'Some rain' : 'Wet'}${singleDay ? '' : ` · ${Math.round(typical.wetDayShare * 100)}% of days wet`}`),
    reason('wind', 'Wind', typical.windKmh, 'km/h', `${Math.round(typical.windKmh)} km/h`, typical.windKmh <= 15 ? 'Light wind' : typical.windKmh <= 25 ? 'Breezy' : typical.windKmh <= 40 ? 'Windy' : 'Strong wind'),
    reason('humidity', 'Humidity', typical.dewPointC, '°C dew point', `${typical.dewPointC.toFixed(1)} °C dew point`, humidityWord(typical.dewPointC)),
    reason('sun', 'Sun', typical.sunnyShare * 100, '% of daylight', `${Math.round(typical.sunnyShare * 100)}% of daylight`, typical.sunnyShare >= 0.8 ? 'Mostly sunny' : typical.sunnyShare >= 0.4 ? 'Sun and cloud' : 'Mostly cloudy'),
  );
  return reasons;
}

export function placeCardSummary(place, judgement, period) {
  const singleDay = period.kind === 'forecast' && period.day !== undefined;
  const totalDays = judgement.countedDays ?? judgement.totalDays;
  const word = `${judgement.band}${singleDay ? '' : ' most days'}`;
  const days = {nice: judgement.niceDayCount, perfect: judgement.perfectDayCount, awful: judgement.awfulDayCount, total: totalDays};
  return {
    name: place.name, region: place.region, word, score: Math.floor(judgement.meanScore), days,
    sentence: `${place.name} ${period.when}: ${word}.${singleDay ? '' : ` Nice on ${days.nice} of ${totalDays} days; perfect on ${days.perfect}; awful on ${days.awful}.`}`,
    reasons: placeReasons(judgement.typical, singleDay),
  };
}
