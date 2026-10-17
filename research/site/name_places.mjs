// Written by AI on 2026.10.17 by model GPT-6.1 Sol.
// Updates an existing English place list from the cached GeoNames cities file.
// Full rebuilds apply the same table in hosting/sites/places.mjs.
import { readFile, writeFile } from 'node:fs/promises';
import { applyPlaceNames, PALESTINE_PLACES, GOLAN_PLACES } from '../../hosting/sites/palestine-places.mjs';
const [input = 'artifacts/places.tsv', citiesFile = '.cache/geonames/cities1000.txt'] = process.argv.slice(2);
const cities = (await readFile(citiesFile, 'utf8')).trim().split('\n').map(line => line.split('\t'));
const byCoordinate = new Map(cities.map(row => [`${Number(row[4])},${Number(row[5])}`, row]));
const result = (await readFile(input, 'utf8')).trim().split('\n').map(line => {
  const [name, plainName, region, latitude, longitude, population, geonamesPlainName, searchAliases, linkAliases, geonamesId, countryCode] = line.split('\t');
  const source = byCoordinate.get(`${Number(latitude)},${Number(longitude)}`);
  if (!source || !['IL', 'PS'].includes(source[8])) return line;
  if (geonamesId) {
    const updatedName = GOLAN_PLACES.get(geonamesId) ?? PALESTINE_PLACES.get(geonamesId);
    return updatedName ? [updatedName, updatedName, GOLAN_PLACES.has(geonamesId) ? 'Syria' : 'Palestine', latitude, longitude, population, geonamesPlainName, searchAliases, linkAliases, geonamesId, countryCode].join('\t') : null;
  }
  const place = applyPlaceNames({ name, plainName, region, latitude, longitude, population, geonamesPlainName }, source);
  return place ? [place.name, place.plainName, place.region, latitude, longitude, population, geonamesPlainName, place.searchAliases, place.linkAliases, place.geonamesId, place.countryCode].join('\t') : null;
}).filter(Boolean);
await writeFile(input, result.join('\n'));
console.log(`Arabic place names applied: ${result.length} places -> ${input}`);
