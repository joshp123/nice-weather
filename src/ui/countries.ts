// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
const countryCodes = 'AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW'.split(' ');
const countryNames = new Intl.DisplayNames(['en'], { type: 'region' });
const countryCodeByName = new Map(countryCodes.map(code => [countryNames.of(code)?.toLowerCase(), code]));
for (const [name, code] of Object.entries({ 'palestine': 'PS', 'palestinian territory': 'PS', 'united states': 'US', 'united kingdom': 'GB', 'russia': 'RU', 'south korea': 'KR', 'north korea': 'KP', 'iran': 'IR', 'syria': 'SY', 'laos': 'LA', 'taiwan': 'TW', 'vietnam': 'VN', 'tanzania': 'TZ', 'bolivia': 'BO', 'venezuela': 'VE', 'moldova': 'MD', 'brunei': 'BN', 'cape verde': 'CV', 'czech republic': 'CZ', 'swaziland': 'SZ', 'ivory coast': 'CI', 'democratic republic of the congo': 'CD', 'republic of the congo': 'CG', 'réunion': 'RE', 'turkey': 'TR' })) countryCodeByName.set(name, code);

export function countryFlag(place: { region?: string; slug?: string | null }): string {
  const countryName = place.region?.split(',').at(-1)?.trim().toLowerCase();
  const slugCountry = place.slug?.split('/')[0].replaceAll('-', ' ');
  const code = countryName && /^[a-z]{2}$/.test(countryName) ? countryName.toUpperCase() : countryCodeByName.get(countryName) ?? countryCodeByName.get(slugCountry);
  return code ? Array.from(code, letter => String.fromCodePoint(127397 + letter.charCodeAt(0))).join('') : '';
}
