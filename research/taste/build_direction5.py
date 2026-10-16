# Written by AI on 2026.10.17 by model claude opus 5.5. Builds the product-direction page: daylight t-shirt hours plus four limits, live sliders.
import importlib.util, json, pathlib, pickle
import numpy as np
O = pathlib.Path('/tmp/nwf-agents/round3/opus')
src = open(O/'export_sliders.py').read().split('blob = ')[0]
g = {'__name__': 'x'}; exec(compile(src, 'export', 'exec'), g)
P = dict(g['places']); SUN_HOURS, SHADE_HOURS = g['SUN_HOURS'], g['SHADE_HOURS']
DEW, RAIN, WIND, DAYLIGHT, MONTH = g['DEW'], g['RAIN'], g['WIND'], g['DAYLIGHT_COLUMN'], g['MONTH']
spec = importlib.util.spec_from_file_location('v5', O/'v5.py'); v5 = importlib.util.module_from_spec(spec); spec.loader.exec_module(v5)
DEFAULT = dict(low=21.0, high=30.0, evening=12.0, humidity=20.0, rain=1.0, wind=35.0)
DJF = [12, 1, 2]

def nice_mask(name, taste):
    """The page's rule on the page's compact data: the Python twin of niceDays() in the page."""
    t = {**DEFAULT, **taste}
    f = P[name].astype(float) + 0.5; hours = np.arange(12)
    unpack = lambda cols, keys: np.stack([np.interp(hours, keys, r) for r in f[:, cols] / 3 - 25])
    sun = unpack(slice(0, len(SUN_HOURS)), SUN_HOURS); shade = unpack(slice(len(SUN_HOURS), DEW), SHADE_HOURS)
    return v5.score_from_felt(sun, shade, P[name][:, DAYLIGHT].astype(int), f[:, DEW] / 3 - 25, ((f[:, RAIN] - 0.5) / 50) ** 2, f[:, WIND] / 2, t) >= 55

def nice(name, taste={}, months=None):
    ok = nice_mask(name, taste); m = P[name][:, MONTH]; years = len(ok) / 365.25
    return round(float(ok.sum()) / years) if months is None else round(float((ok & np.isin(m, months)).sum()) / years)

def share(name, month, taste={}):
    ok = nice_mask(name, taste); return round(100 * float(ok[P[name][:, MONTH] == month].mean()))

def full_hourly_counts():
    inputs = pickle.load(open(O/'inputs_2016_2025.pkl', 'rb')); where, daylight = pickle.load(open(O/'daylight.pkl', 'rb')); out = {}
    for n, (T, dew, ws, dni, rain, m) in inputs.items():
        if n == 'San Francisco': T = g['station_temperatures'](O/'candidates/san_francisco.json', 'America/Los_Angeles', '/tmp/nwf-agents/round3/bayarea/sf.dly')
        sun, shade, d = v5.felt(T, dew, ws, dni)
        ok = v5.score_from_felt(sun, shade, daylight[n][:len(m)], d.mean(1), rain, ws.mean(1), DEFAULT) >= 55; out[n] = round(float(ok.sum()) / (len(m) / 365.25))
    return out

if __name__ == '__main__':
    payload = json.load(open(O/'slider_data.json'))
    marked = {'San Diego': 'yardstick', 'Los Angeles': 'yardstick', 'San Francisco, downtown station': 'yardstick', 'Austin': 'your trip'}
    year = {n: nice(n) for n in P}
    ranked = sorted(year, key=lambda n: -year[n])
    print('ranking at defaults:', [(n.split(',')[0], year[n]) for n in ranked])

    best = f'''<p>At your taste, the places closest to SoCal at its best, all year, are the Canary Islands (Gran Canaria {year["Las Palmas, Gran Canaria"]} nice days, south Tenerife {year["Los Cristianos, south Tenerife"]}) and high, dry cities in the tropics (Nairobi at 1,800 m: {year["Nairobi, Kenya"]}; Guadalajara at 1,550 m: {year["Guadalajara, Mexico"]}): warm every afternoon and rarely muggy. San Diego gets {year["San Diego"]} and Los Angeles {year["Los Angeles"]}. San Francisco, on real temperatures, gets {year["San Francisco, downtown station"]}. Honolulu falls to {year["Honolulu"]}: its dew point sits around 20 to 21 °C from May to October. These are 31 places I picked; the map ranks every square on land, including places nobody thought of.</p>'''

    shot = dict(low=21, high=37, evening=19, humidity=21, rain=None, wind=30)
    only_heat = dict(evening=None, rain=None, wind=None)
    rows = [('No limit', None), ('24 °C', 24.0), ('22 °C', 22.0), ('20 °C (your default)', 20.0)]
    singapore = '<p>Humidity now does what it says. Singapore, nice days a year, with the walk home, rain and wind limits off so only heat and humidity count:</p><table><tr><th>Dew point limit</th><th class="num">T-shirt up to 30 °C</th><th class="num">up to 34 °C</th><th class="num">up to 37 °C</th></tr>'
    for label, h in rows:
        singapore += f'<tr><td>{label}</td>' + ''.join(f'<td class="num">{nice("Singapore", dict(only_heat, humidity=h, high=hi))}</td>' for hi in (30, 34, 37)) + '</tr>'
    heat_alone = nice("Singapore", dict(only_heat, humidity=None))
    n_shot = nice("Singapore", shot)
    singapore += f'</table><p>With no humidity limit, Singapore still gets {heat_alone} days at 30 °C: on those, most daylight hours feel about 30 °C or less in the shade. The humidity limit is what rules it out, as it should, because muggy air is its own nuisance, not just heat. The settings in your screenshot (21 to 37 °C, 21:00 at least 19 °C, dew point 21 °C, no rain limit, wind 30 km/h) now give Singapore {n_shot} nice day{"" if n_shot == 1 else "s"} a year and Bangkok {nice("Bangkok", shot)}; before, 365 and 359.</p>'

    ct30, ct35 = round(100 * nice('Cape Town', dict(wind=30), DJF) / 90), round(100 * nice('Cape Town', {}, DJF) / 90)

    au = {m: share('Austin', m) for m in range(1, 13)}; summer = sorted(au[m] for m in (6, 7, 8)); winter = sorted(au[m] for m in DJF)
    malaga_nov = share('Málaga', 11)
    austin = f'''<p>Austin gets {year["Austin"]} nice days a year at your taste, and you went in its best month. <b>October</b>: {au[10]}% of days nice, highs around 30 °C and still 24 °C at 21:00, t-shirt day and night. <b>November</b>: {au[11]}%, highs around 23 °C and 14 °C by 21:00. March and April come close to October ({au[3]}% and {au[4]}%).</p>
<p><b>June to August</b> is too hot and humid: {summer[0]} to {summer[-1]}% nice, still 33 °C at 21:00, with a dew point of 21 °C. <b>December to February</b> ({winter[0]} to {winter[-1]}%) has highs of 17 to 20 °C and cold evenings.</p>
<p>You expected Austin in November to beat Málaga for t-shirt time. {"It does" if au[11] > malaga_nov + 2 else "They are about level" if abs(au[11] - malaga_nov) <= 2 else "It doesn't"}: {au[11]}% of November days are nice in Austin and {malaga_nov}% in Málaga. Both feel about 14 °C by 21:00.</p>'''

    sd_djf = nice('San Diego', {}, DJF); la_djf = nice('Los Angeles', {}, DJF)
    socal = f'''<p>From June to October San Diego and Los Angeles are t-shirt weather nearly all day and 20 to 25 °C at 21:00. Winter is the gap. Middays are fine (San Diego at 13:00 in January feels 22 °C in the sun), but the sun sets around 17:00, the late afternoon turns cool, and 21:00 feels 11 °C. So December to February has {sd_djf} nice days out of 90 in San Diego and {la_djf} in Los Angeles at your defaults. Rain on about one January day in ten does the rest. SoCal's winter is mild, sunny middays, not shorts all day.</p>'''

    ev_rows = [('16 °C: comfortable in a t-shirt', 16.0), ('12 °C: cool in a t-shirt, fine for a walk', 12.0), ('8 °C: a light layer', 8.0), ('No limit', None)]
    evening_table = '<table><tr><th>Walk home at 21:00 feels at least</th><th class="num">San Diego, Dec to Feb</th><th class="num">San Diego, year</th><th class="num">South Tenerife, Dec to Feb</th><th class="num">Cape Town, Dec to Feb</th></tr>'
    for label, e in ev_rows:
        t = dict(evening=e)
        evening_table += f'<tr><td>{label}</td><td class="num">{nice("San Diego", t, DJF)} of 90</td><td class="num">{nice("San Diego", t)}</td><td class="num">{nice("Los Cristianos, south Tenerife", t, DJF)} of 90</td><td class="num">{nice("Cape Town", t, DJF)} of 90</td></tr>'
    evening_table += '</table>'

    full = full_hourly_counts()
    name_of = {'Santa Cruz': 'Santa Cruz, north-east Tenerife', 'Chania': 'Chania, Crete', 'San Francisco': 'San Francisco, downtown station'}
    for n in P:
        short = n.split(',')[0]; name_of.setdefault(short, n); name_of.setdefault(short.replace('í', 'i'), n)
    gaps = {k: year[name_of[k]] - c for k, c in full.items()}
    worst = max(abs(x) for x in gaps.values()); within1 = sum(abs(x) <= 1 for x in gaps.values())
    compact_check = f'never more than {worst} nice days a year apart at the 31 places ({within1} within 1)'
    print('compact minus full hourly:', gaps)

    months = ''.join(f'<th class="month">{m}</th>' for m in 'J F M A M J J A S O N D'.split())
    page = (O/'direction_template.html').read_text()
    for k, val in (('__MONTH_HEADERS__', months), ('__BEST_TEXT__', best), ('__SINGAPORE__', singapore), ('__CT_WIND_30__', str(ct30)), ('__CT_WIND_35__', str(ct35)),
                   ('__AUSTIN__', austin), ('__SOCAL__', socal), ('__BAYAREA__', (O/'bayarea_section.html').read_text()), ('__EVENING_TABLE__', evening_table),
                   ('__COMPACT_CHECK__', compact_check), ('__MARKED__', json.dumps(marked, ensure_ascii=False)), ('__PAYLOAD__', json.dumps(payload, ensure_ascii=False))):
        assert k in page, k; page = page.replace(k, val)
    assert '__' not in page.replace('__proto__', ''), 'unfilled placeholder'
    pathlib.Path('/Users/josh/code/nice-weather-finder/docs/product-direction.html').write_text(page)
    checks = {'default': DEFAULT, 'other': dict(low=19, high=32, evening=None, humidity=22, rain=5.0, wind=45), 'screenshot': shot}
    json.dump({k: {n: nice(n, t) for n in P} for k, t in checks.items()}, open(O/'slider_checks.json', 'w'), ensure_ascii=False)
    print('written', len(page), '| Cape Town DJF wind 30/35:', ct30, ct35, '| SD DJF', sd_djf, 'LA', la_djf, '| Austin', au, 'Malaga Nov', malaga_nov)
