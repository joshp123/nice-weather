// Written by AI on 2026.10.16 by model claude opus 5.5.
// Builds the place list the Worker uses to name a clicked spot and to search.
// Source: GeoNames cities with 1,000 or more people (CC BY 4.0), downloaded into .cache/geonames.
// Output: one tab-separated line per place, largest first: name, plain name, region, latitude, longitude, population.
import { readFile, writeFile } from 'node:fs/promises';

const [sourceDirectory = '.cache/geonames', output = 'artifacts/places.tsv'] = process.argv.slice(2);
const lines = async (name) => (await readFile(`${sourceDirectory}/${name}`, 'utf8')).split('\n').filter((line) => line && !line.startsWith('#'));

const countries = new Map((await lines('countryInfo.txt')).map((line) => { const fields = line.split('\t'); return [fields[0], fields[4]]; }));
const regions = new Map((await lines('admin1CodesASCII.txt')).map((line) => { const [code, name] = line.split('\t'); return [code, name]; }));

const places = (await lines('cities1000.txt')).map((line) => {
  const field = line.split('\t');
  const country = countries.get(field[8]) ?? field[8];
  const region = regions.get(`${field[8]}.${field[10]}`);
  return {
    name: field[1],
    plainName: field[2],
    region: region && region !== field[1] ? `${region}, ${country}` : country,
    latitude: Number(field[4]),
    longitude: Number(field[5]),
    population: Number(field[14]) || 0,
  };
});
places.sort((left, right) => right.population - left.population);
await writeFile(output, places.map((place) => [place.name, place.plainName, place.region, place.latitude, place.longitude, place.population].join('\t')).join('\n'));
console.log(`${places.length} places -> ${output}`);
