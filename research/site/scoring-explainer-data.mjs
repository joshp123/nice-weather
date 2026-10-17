// Written by AI on 2026.10.17 by model GPT-6.1 Sol.
// Read the published weather files at build time so the explanation follows each data rebuild.
import { readFileSync } from 'node:fs';

export function scoringExplainerData(root = 'artifacts/v6') {
  const metadata = JSON.parse(readFileSync(`${root}/climate/meta.json`, 'utf8'));
  const chunkHours = 8736; // research/era5/fetch_era5.py: HOURS, starting at 1940-01-01T00.
  const epoch = Date.UTC(1940, 0, 1);
  const formatDate = milliseconds => new Date(milliseconds).toLocaleDateString('en-GB', { day: 'numeric', month: 'long', year: 'numeric', timeZone: 'UTC' });
  const spans = metadata.chunks.map(chunk => {
    const start = epoch + chunk * chunkHours * 3600000;
    // research/climate-v6/pipeline.py: chunk_dates keeps local days 2 through 361.
    return `${formatDate(start + 2 * 86400000)} to ${formatDate(start + 361 * 86400000)}`;
  });
  const cells = readFileSync(`${root}/cells.bin`);
  const months = new Map();
  const places = {
    rotterdam: { name: 'Rotterdam', latitude: 51.92, longitude: 4.48 },
    malaga: { name: 'Málaga', latitude: 36.72, longitude: -4.42 },
    singapore: { name: 'Singapore', latitude: 1.29, longitude: 103.85 },
    vegas: { name: 'Las Vegas', latitude: 36.17, longitude: -115.14 },
    puntaArenas: { name: 'Punta Arenas', latitude: -53.16, longitude: -70.91 },
  };
  function reference(placeName, month, metric) {
    const place = places[placeName];
    const targetRow = Math.floor(90 - place.latitude), targetColumn = Math.floor(place.longitude + 180);
    let ordinal = 0, distance = Infinity;
    for (let index = 0; index < cells.length / 4; index++) {
      const deltaRow = cells.readUInt16LE(index * 4) - targetRow;
      const deltaColumn = cells.readUInt16LE(index * 4 + 2) - targetColumn;
      const squaredDistance = deltaRow ** 2 + deltaColumn ** 2;
      if (squaredDistance < distance) { ordinal = index; distance = squaredDistance; }
    }
    if (!months.has(month)) months.set(month, readFileSync(`${root}/climate/month-${String(month).padStart(2, '0')}.bin`));
    const records = months.get(month);
    let total = 0;
    for (let sample = 0; sample < 8; sample++) {
      const offset = (ordinal * 8 + sample) * 15;
      const temperature = byte => records[offset + byte] / 3 - 30;
      if (metric === 'warmth') total += temperature(2);
      if (metric === 'heat') total += temperature(7);
      if (metric === 'evening') total += temperature(9);
      if (metric === 'humidity') total += temperature(10);
      if (metric === 'rain') total += (records[offset + 11] / 50) ** 2;
      if (metric === 'wind') total += records[offset + 12] / 2;
      if (metric === 'sunshine') total += records[offset + 13] / Math.max(1, records[offset + 14]);
    }
    const monthName = new Date(Date.UTC(2000, month - 1)).toLocaleDateString('en-GB', { month: 'short', timeZone: 'UTC' });
    return { label: `${place.name} · ${monthName}`, value: total / 8 };
  }
  return {
    spans, days: metadata.daysPerMonthMinimum.reduce((total, days) => total + days, 0), spanCount: metadata.chunks.length,
    references: {
      warmth: [reference('rotterdam', 1, 'warmth'), reference('malaga', 7, 'warmth')],
      heat: [reference('singapore', 4, 'heat'), reference('vegas', 7, 'heat')],
      evening: [reference('rotterdam', 1, 'evening'), reference('malaga', 7, 'evening')],
      rain: [reference('malaga', 7, 'rain'), reference('rotterdam', 11, 'rain')],
      wind: [reference('singapore', 11, 'wind'), reference('puntaArenas', 1, 'wind')],
      humidity: [reference('rotterdam', 1, 'humidity'), reference('singapore', 4, 'humidity')],
      sunshine: [reference('rotterdam', 11, 'sunshine'), reference('malaga', 7, 'sunshine')],
    },
  };
}
