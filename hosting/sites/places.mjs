// Written by AI on 2026.10.16 by model claude opus 5.5; English names added 2026.10.17.
// Builds the place list the Worker uses to name a clicked spot, to search and to make links.
// Sources: GeoNames cities with 1,000 or more people, and GeoNames alternate names for each place's English name
// (CC BY 4.0), downloaded into the given directory.
// Output: one tab-separated line per place, largest first: name (English where GeoNames has one), plain name (ASCII of
// that), region, latitude, longitude, population, and GeoNames' own ASCII name (for links made before English names).
import { createReadStream } from 'node:fs';
import { readFile, writeFile } from 'node:fs/promises';
import { createInterface } from 'node:readline';

const [sourceDirectory = '.cache/geonames', output = 'artifacts/places.tsv', alternateNames = `${sourceDirectory}/alternateNamesV2.txt`] = process.argv.slice(2);
const lines = async (name) => (await readFile(`${sourceDirectory}/${name}`, 'utf8')).split('\n').filter((line) => line && !line.startsWith('#'));
const plain = (text) => text.normalize('NFD').replace(/[̀-ͯ]/g, '');

const countries = new Map((await lines('countryInfo.txt')).map((line) => { const fields = line.split('\t'); return [fields[0], fields[4]]; }));
const regions = new Map((await lines('admin1CodesASCII.txt')).map((line) => { const [code, name] = line.split('\t'); return [code, name]; }));
const cities = (await lines('cities1000.txt')).map((line) => line.split('\t'));
const wanted = new Set(cities.map((field) => field[0]));

// English names: GeoNames' preferred English name, else its first everyday English name.
const preferredEnglish = new Map();
const anyEnglish = new Map();
for await (const line of createInterface({ input: createReadStream(alternateNames) })) {
  const [, id, language, name, preferred, short, colloquial, historic] = line.split('\t');
  if (language !== 'en' || !wanted.has(id) || colloquial === '1' || historic === '1') continue;
  if (preferred === '1' && !preferredEnglish.has(id)) preferredEnglish.set(id, name);  // GeoNames marks Seville both preferred and short
  if (short !== '1' && !anyEnglish.has(id)) anyEnglish.set(id, name);
}

const places = cities.map((field) => {
  const country = countries.get(field[8]) ?? field[8];
  const region = regions.get(`${field[8]}.${field[10]}`);
  const name = preferredEnglish.get(field[0]) ?? anyEnglish.get(field[0]) ?? field[1];
  return {
    name, plainName: plain(name),
    region: region && region !== field[1] ? `${region}, ${country}` : country,
    latitude: Number(field[4]), longitude: Number(field[5]), population: Number(field[14]) || 0,
    geonamesPlainName: field[2],
  };
});
places.sort((left, right) => right.population - left.population);
await writeFile(output, places.map((place) => [place.name, place.plainName, place.region, place.latitude, place.longitude, place.population, place.geonamesPlainName].join('\t')).join('\n'));
console.log(`${places.length} places, ${places.filter((place) => plain(place.name) !== place.geonamesPlainName).length} renamed to English -> ${output}`);
