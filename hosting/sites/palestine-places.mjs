// Written by AI on 2026.10.17 by model GPT-6.1 Sol.
// Places in Palestine use their Arabic names. This is the curated list of towns we name.
// Names are plain-letter transliterations of GeoNames' Arabic names, at each cited GeoNames ID.
// Historic city spellings also appear in the Institute for Palestine Studies' town index:
// https://www.palquest.org/en/placesabout . Other IL/PS entries are omitted.
export const PALESTINE_PLACES = new Map([
  ['281184', 'Al-Quds'], // https://www.geonames.org/281184/ (Al-Quds)
  ['293397', 'Yafa'], // https://www.geonames.org/293253/ (Jaffa / Yafa, part of Tel Aviv-Yafo)
  ['294801', 'Hayfa'], // https://www.geonames.org/294801/ (Haifa / Hayfa)
  ['295721', 'Akka'], // https://www.geonames.org/295721/ (Acre / Akka)
  ['285066', 'Al-Khalil'], // https://www.geonames.org/285066/ (Hebron / Al Khalil)
  ['284315', 'Bayt Lahm'], // https://www.geonames.org/284315/ (Bethlehem / Bayt Lahm)
  ['281133', 'Ghazza'], // https://www.geonames.org/281133/ (Gaza / Ghazza)
  ['295530', "Bir as-Sab'"], // https://www.geonames.org/295530/ (Beersheba / Bir as Sab)
  ['294098', 'An-Nasira'], // https://www.geonames.org/294098/ (Nazareth / An Nasirah)
  ['293100', 'Safad'], // https://www.geonames.org/293100/ (Safed / Safad)
  ['293322', 'Tabariyya'], // https://www.geonames.org/293322/ (Tiberias / Tabariya)
  ['294421', 'Al-Lydd'], // https://www.geonames.org/294421/ (Lod / Al Ludd)
  ['293768', 'Ar-Ramla'], // https://www.geonames.org/293768/ (Ramla / Ar Ramlah)
  ['295435', 'Baysan'], // https://www.geonames.org/295435/ (Bet Shean / Baysan)
  ['282615', 'Nablus'], // https://www.geonames.org/282615/
  ['282239', 'Ramallah'], // https://www.geonames.org/282239/
  ['285108', 'Al-Bira'], // https://www.geonames.org/285108/
  ['283506', 'Jenin'], // https://www.geonames.org/283506/
  ['281577', 'Tulkarm'], // https://www.geonames.org/281577/
  ['282457', 'Qalqilya'], // https://www.geonames.org/282457/
  ['284899', 'Ariha'], // https://www.geonames.org/284899/ (Jericho / Ariha)
  ['281581', 'Tubas'], // https://www.geonames.org/281581/
  ['282039', 'Salfit'], // https://www.geonames.org/282039/
  ['281102', 'Rafah'], // https://www.geonames.org/281102/
  ['281124', 'Khan Yunis'], // https://www.geonames.org/281124/
  ['281129', 'Jabalya'], // https://www.geonames.org/281129/
  ['281141', 'Dayr al-Balah'], // https://www.geonames.org/281141/
  ['281145', 'Bayt Lahya'], // https://www.geonames.org/281145/
  ['281146', 'Bayt Hanun'], // https://www.geonames.org/281146/
  ['281148', 'Bani Suhayla'], // https://www.geonames.org/281148/
  ['281109', 'An-Nusayrat'], // https://www.geonames.org/281109/
  ['281161', 'Al-Burayj'], // https://www.geonames.org/281161/
  ['281292', 'Yatta'], // https://www.geonames.org/281292/
  ['284011', 'Dura'], // https://www.geonames.org/284011/
  ['283806', 'Halhul'], // https://www.geonames.org/283806/
  ['284304', 'Bayt Sahur'], // https://www.geonames.org/284304/
  ['284324', 'Bayt Jala'], // https://www.geonames.org/284324/
  ['293286', 'Umm al-Fahm'], // https://www.geonames.org/293286/
  ['293655', 'Sakhnin'], // https://www.geonames.org/293655/
  ['293539', "Shafa 'Amr"], // https://www.geonames.org/293539/
  ['293426', 'Tamra'], // https://www.geonames.org/293426/
  ['293896', 'Qalansuwa'], // https://www.geonames.org/293896/
  ['294604', 'Kafr Qasim'], // https://www.geonames.org/294604/
  ['294608', 'Kafr Manda'], // https://www.geonames.org/294608/
  ['294610', 'Kafr Kanna'], // https://www.geonames.org/294610/
  ['294626', 'Jisr az-Zarqa'], // https://www.geonames.org/294626/
  ['294642', 'Isfiya'], // https://www.geonames.org/294642/
  ['295365', 'Daliyat al-Karmil'], // https://www.geonames.org/295365/
  ['295772', 'Abu Ghawsh'], // https://www.geonames.org/295772/
]);

// These GeoNames IL entries are in the Golan Heights and belong to Syria.
// The same GeoNames records supply the Arabic names; contemporary settlement names are omitted.
export const GOLAN_PLACES = new Map([
  ['170378', 'Fiq'], // https://www.geonames.org/170378/
  ['172119', 'Ayn Qunya'], // https://www.geonames.org/172119/
  ['173550', 'Al-Butayha'], // https://www.geonames.org/173550/
  ['173601', 'Al-Khushniyya'], // https://www.geonames.org/173601/
  ['173882', 'Ghajar'], // https://www.geonames.org/173882/
  ['443093', 'Qasrin'], // https://www.geonames.org/443093/
]);

export function applyPlaceNames(place, geonames) {
  const [id, , geonamesPlainName, , , , , , countryCode] = geonames;
  if (countryCode !== 'IL' && countryCode !== 'PS') return place;
  const name = GOLAN_PLACES.get(id) ?? PALESTINE_PLACES.get(id);
  if (!name) return null;
  const oldNames = [...new Set([place.plainName, geonamesPlainName])];
  return { ...place, name, plainName: name, region: GOLAN_PLACES.has(id) ? 'Syria' : 'Palestine',
    searchAliases: [...oldNames, ...(id === '293397' ? ['Jaffa', 'Tel Aviv-Yafo'] : [])].join('|'),
    geonamesId: id, countryCode: GOLAN_PLACES.has(id) ? 'SY' : 'PS' };
}
