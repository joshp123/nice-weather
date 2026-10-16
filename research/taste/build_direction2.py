# Written by AI on 2026.10.17 by model claude opus 5.5. Builds the product-direction page with live sliders.
import json, pathlib, sys
import numpy as np
O = pathlib.Path('/tmp/nwf-agents/round3/opus')
src = open(O/'export_sliders.py').read().split('blob = ')[0]
g = {'__name__': 'x'}; exec(compile(src, 'export', 'exec'), g)
P = dict(g['places']); v = g['v']; KEY = g['KEY']
DEFAULT = dict(low=21, high=30, evening=15, humidity=16, rain=3, wind=25)

def nice_mask(name, t):
    f = P[name].astype(float) + 0.5; f[:, 11] -= 0.5; f[:, 13:] -= 0.5; hours = np.arange(12); ip = lambda a: np.stack([np.interp(hours, KEY, r) for r in a])
    sun, shade = ip(f[:, 0:5] / 3 - 25), ip(f[:, 5:10] / 3 - 25)
    s = np.minimum(np.clip(1 - (t['low'] - sun) / 5, 0, 1), np.clip(1 - (shade - t['high']) / 10, 0, 1)).mean(1) * 100
    if t['rain'] is not None: s = s * v.rain_factor((f[:, 11] / 50) ** 2 * 3 / t['rain'])
    if t['wind'] is not None: s = s * v.wind_factor(f[:, 12] / 2 - (t['wind'] - 25))
    if t['humidity'] is not None: s = s * np.interp(f[:, 10] / 3 - 25 - (t['humidity'] - 16), [16, 18, 20, 22, 24, 26, 28], [1, .95, .8, .55, .3, .1, 0])
    if t['evening'] is not None: s = np.where(shade[:, -1] < t['evening'], np.minimum(s, 54), s)
    return s >= 55

def nice(name, t, months=None):
    f = P[name].astype(float) + 0.5; f[:, 11] -= 0.5; f[:, 13:] -= 0.5; hours = np.arange(12); ip = lambda a: np.stack([np.interp(hours, KEY, r) for r in a])
    sun, shade = ip(f[:, 0:5] / 3 - 25), ip(f[:, 5:10] / 3 - 25)
    s = np.minimum(np.clip(1 - (t['low'] - sun) / 5, 0, 1), np.clip(1 - (shade - t['high']) / 10, 0, 1)).mean(1) * 100
    if t['rain'] is not None: s = s * v.rain_factor((f[:, 11] / 50) ** 2 * 3 / t['rain'])
    if t['wind'] is not None: s = s * v.wind_factor(f[:, 12] / 2 - (t['wind'] - 25))
    if t['humidity'] is not None: s = s * np.interp(f[:, 10] / 3 - 25 - (t['humidity'] - 16), [16, 18, 20, 22, 24, 26, 28], [1, .95, .8, .55, .3, .1, 0])
    if t['evening'] is not None: s = np.where(shade[:, -1] < t['evening'], np.minimum(s, 54), s)
    ok = s >= 55; m = f[:, 14]
    if months is None: return round(float(ok.sum()) / (len(f) / 365.25))
    return round(float((ok & np.isin(m, months)).sum()) / (len(f) / 365.25))

def share(name, t, month):
    f = P[name][:, 14]; days = nice_mask(name, t)
    return round(100 * float(days[f == month].mean()))

if __name__ == '__main__':
    payload = json.load(open(O/'slider_data.json'))
    marked = {'San Diego': 'yardstick', 'Los Angeles': 'yardstick', 'San Francisco': 'yardstick', 'Austin': 'your trip'}
    sd_winter = nice('San Diego', DEFAULT, [12, 1, 2]); sd_winter_any = nice('San Diego', {**DEFAULT, 'evening': None}, [12, 1, 2])
    la_winter = nice('Los Angeles', DEFAULT, [12, 1, 2]); la_winter_any = nice('Los Angeles', {**DEFAULT, 'evening': None}, [12, 1, 2])
    mal = P['Málaga'].astype(float); mal_nov = mal[mal[:, 14] == 11]
    malaga_nov_pct = share('Málaga', DEFAULT, 11)
    malaga_nov_evening = round(float(np.median((mal_nov[:, 9] + 0.5) / 3 - 25)))
    au = {m: share('Austin', DEFAULT, m) for m in range(1, 13)}
    summer = sorted(au[m] for m in (6, 7, 8)); winter = sorted(au[m] for m in (12, 1, 2))
    austin = f'''<p>Austin gets {nice('Austin', DEFAULT)} nice days a year at your taste, and you picked its best month. <b>October</b>: {au[10]}% of days nice, highs around 30 °C and still 24 °C at 9 pm, so t-shirt day and night. <b>November</b> drops to {au[11]}%: highs around 23 °C but 14 °C at 9 pm, so warm middays and a layer for the evening. March and April come close to October ({au[3]}% and {au[4]}%).</p>
<p><b>June to August</b> is too hot and too humid: {summer[0]} to {summer[-1]}% nice, still 33 °C at 9 pm, with a dew point of 21 °C. <b>December to February</b> ({winter[0]} to {winter[-1]}%) has highs of 17 to 20 °C and cold evenings.</p>
<p>You expected Austin in November to beat Málaga for t-shirt time. {'It does, narrowly' if au[11] > malaga_nov_pct else 'It roughly ties'}: {au[11]}% of November days against Málaga's {malaga_nov_pct}%, and both need a layer by 9 pm (Málaga about {malaga_nov_evening} °C). Neither is shorts all day.</p>'''
    socal = f'''<p>San Diego gets {nice('San Diego', DEFAULT)} nice days a year at your taste and Los Angeles {nice('Los Angeles', DEFAULT)}. From June to October it is t-shirt weather nearly all day and 20 to 25 °C at 9 pm. Winter middays are fine too: San Diego at 1 pm in January feels 22 °C in the sun.</p>
<p>The evening is what stops them in winter. San Diego feels about 12 °C at 9 pm in January, so December to February has {sd_winter} nice days out of 90 at your setting (Los Angeles {la_winter}). Slide the walk home to "don't mind" and that becomes {sd_winter_any} ({la_winter_any}). Rain on about one January day in ten does the rest. So SoCal's winter is sunny, mild middays with cool evenings, and the evening slider is the choice that decides how good it looks.</p>'''
    rows = [('T-shirt (18 °C)', 18), ('A light layer (15 °C)', 15), ('A jacket (12 °C)', 12), ("Don't mind", None)]
    evening_table = '<table><tr><th>Walk home at 9 pm</th><th class="num">San Diego, Dec to Feb</th><th class="num">San Diego, year</th><th class="num">South Tenerife, Dec to Feb</th><th class="num">Cape Town, Dec to Feb</th></tr>'
    for label, e in rows:
        t = {**DEFAULT, 'evening': e}
        evening_table += f'<tr><td>{label}</td><td class="num">{nice("San Diego", t, [12, 1, 2])} of 90</td><td class="num">{nice("San Diego", t)}</td><td class="num">{nice("Los Cristianos, south Tenerife", t, [12, 1, 2])} of 90</td><td class="num">{nice("Cape Town", t, [12, 1, 2])} of 90</td></tr>'
    evening_table += '</table>'
    bay = (O/'bayarea_section.html').read_text() if (O/'bayarea_section.html').exists() else '<p class="muted">Being measured.</p>'
    months = ''.join(f'<th class="month">{m}</th>' for m in 'J F M A M J J A S O N D'.split())
    page = (O/'direction_template.html').read_text()
    for k, val in (('__MONTH_HEADERS__', months), ('__EVENING_TABLE__', evening_table), ('__AUSTIN__', austin), ('__SOCAL__', socal), ('__BAYAREA__', bay), ('__MARKED__', json.dumps(marked, ensure_ascii=False)), ('__PAYLOAD__', json.dumps(payload, ensure_ascii=False))):
        assert k in page, k; page = page.replace(k, val)
    out = pathlib.Path('/Users/josh/code/nice-weather-finder/docs/product-direction.html'); out.write_text(page)
    checks = {'default': DEFAULT, 'other': dict(low=19, high=32, evening=None, humidity=19, rain=6.0, wind=35)}
    json.dump({k: {n: nice(n, t) for n in P} for k, t in checks.items()}, open(O/'slider_checks.json', 'w'), ensure_ascii=False)
    print('written', len(page), 'bytes; SD winter', sd_winter, '->', sd_winter_any, 'LA', la_winter, '->', la_winter_any, 'Malaga Nov %', malaga_nov_pct, 'evening', malaga_nov_evening)
