# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
"""Render exact release comparisons; never substitute a partial probe for a world rank."""
import argparse
import json
from pathlib import Path

def table(headers, rows):
    return '\n'.join(['| '+' | '.join(headers)+' |','| '+' | '.join(['---']*len(headers))+' |']+['| '+' | '.join(str(value) for value in row)+' |' for row in rows])

def main(args):
    comparison=json.loads(args.comparison.read_text()); targets=json.loads(args.targets.read_text())
    indexed={(round(row['latitude']*1e5),round(row['longitude']*1e5)):row for row in targets['records']}
    rows=comparison['places']; out=['<!-- Written by AI on 2026.10.18 by model GPT-6.1 Sol. -->','# Satellite sunshine for named places','',args.status,'',
    '**The no-regression gate is not established.** Sparse exact-date radiation diagnostics worsen monthly scores and nice-day counts at San Francisco and San Diego, plus nice-day counts at Rotterdam, despite improving monthly satellite means. [Control errors and limitations](NAMED_CONTROL_CHECKS.md).','',
    'The same shared place model computes both releases. The correction changes each sampled day\'s sunny fraction before the SUN factor and its hourly direct-radiation warmth. Air highs, 21:00 shade feels-like, dew point, wet days and wind stay identical. Uncovered places and months retain their previous values. Forecast weather is not replaced by monthly climatology.','',
    'The compact default-taste climate records keep eight scores per month. Cards, map labels, headlines and previews aggregate those same records. The native replay applies the same coefficients before offline computation. The JavaScript/Go arithmetic proof covers the same formula.','',
    '## Annual day counts','',table(['Place','Nice before','Nice after','Perfect before','Perfect after'],[[row['place']['validationName'],row['before']['year']['niceDayCount'],row['after']['year']['niceDayCount'],row['before']['year']['perfectDayCount'],row['after']['year']['perfectDayCount']] for row in rows]),'',
    'Counts estimate a typical year from 96 representative days, weighted to 365 calendar days; they are not 365 independently observed days. A lower count after correcting an over-sunny source is not, by itself, a weather-data regression.','',
    'Chiclayo still has 365 nice days after correction: its independent July target is about 64% sunny, much brighter than Lima. The existing taste rule permits warm, partly cloudy days to be nice. Antofagasta still has 151 nice days in November–March, with fewer perfect days. The correction does not force coastal places down the ranking or change the taste rule.','',
    '## Monthly sunny share','',
    'Percent of daylight clock hours after 10:00 meeting the app\'s direct-normal-radiation threshold of 300 W/m². **Before** is the published card\'s monthly fact; **satellite** is the actual fitted independent-source target; **after** is the new card fact. Agreement with the fitted target establishes transport and fitting, not independent source accuracy.','']
    months=['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec']
    for row in rows:
        place=row['place']; target=indexed.get((round(place['latitude']*1e5),round(place['longitude']*1e5)))
        satellite=[month['sunnyShare']*100 if month else None for month in target['months']] if target else [None]*12
        for month,expected in enumerate(satellite):
            if expected is not None and abs(row['after']['months'][month]['sunnyPercent']-expected)>0.001:
                raise ValueError(f"Published monthly sunshine differs from fitted satellite target: {place['slug']}/{month+1}")
        lines=[['Before']+[f"{month['sunnyPercent']:.1f}" for month in row['before']['months']],['Satellite']+['—' if value is None else f'{value:.1f}' for value in satellite],['After']+[f"{month['sunnyPercent']:.1f}" for month in row['after']['months']]]
        out+=['### '+place['validationName'],'',f"Exact named point: {place['latitude']:.5f}, {place['longitude']:.5f}. Source: {target.get('source','see source manifest') if target else 'not covered; unchanged'}.",'',table(['% sunny']+months,lines),'']
    if args.controls:
        controls=json.loads(args.controls.read_text()); proof=[]
        for control in controls['places']:
            row=next(row for row in rows if row['place']['slug']==control['place']['slug'])
            reference=[month['referenceSun'] for month in control['months']]
            old=[month['sunnyPercent']/100 for month in row['before']['months']]
            new=[month['sunnyPercent']/100 for month in row['after']['months']]
            mae=lambda value:100*sum(abs(left-right) for left,right in zip(value,reference))/12
            proof.append([row['place']['validationName'],f'{mae(old):.2f}',f'{mae(new):.2f}',f"{control['dniHourlyMonthlyMaeBefore']:.1f}",f"{control['dniHourlyMonthlyMaeAfter']:.1f}"])
        out+=['## Full-period control check','',
        'These six cities were separately downloaded as complete 2005–2023 SARAH-3 hourly series at their exact named coordinates. Both sunny share and direct-radiation warmth targets were checked. SARAH-3 is independent of ERA5; this full-series check uses the same satellite product as the compact profile source, so it does not establish an independent satellite retrieval error. No city-specific exception is used.','',
        table(['Place','Monthly sunshine error before (pp)','After (pp)','Hourly DNI error before (W/m²)','After (W/m²)'],proof),'',
        'Raw TMY months failed Seville: a selected February was substantially cloudier than the 19-year normal. The common normalization scales each month/hour of every TMY day to the full-period satellite DNI profile, then recomputes sunny hours. This retains daily variation while avoiding the wrong monthly mean.','',
        'The exact-date radiation diagnostic also exposes a remaining limitation: Rotterdam\'s annual nice-day error increases from 3 to 10 days, despite its monthly score error improving from 6.17 to 2.07 points. Seville\'s monthly score error increases by 0.11 points while its annual mean is unchanged. These diagnostics omit unavailable 2024 satellite dates identically in all scenarios. They do not justify an unconditional no-regression claim. [Control checks and the Arona/airport distinction](NAMED_CONTROL_CHECKS.md).','']
    out+=['## World ranking, with one fixed ranking rule','',f"Both columns use the same current ranking module (SHA-256 `{comparison['rankingSha256']}`). Another worker owns the ranking rule. The live baseline screenshot can differ because that code changed independently of this data change.",'']
    for period,ranks in comparison['rankings'].items():
        out+=['### '+period,'']
        if not ranks.get('completeWorldComparison'):
            out+=['**Partial probe: this is not a worldwide ranking comparison.**',''];continue
        before=ranks['before']['places'];after=ranks['after']['places'];length=max(len(before),len(after))
        out+=[table(['Rank','Before','Nice / perfect','After','Nice / perfect'],[[index+1,before[index]['name'] if index<len(before) else '—',f"{before[index]['niceDayCount']} / {before[index]['perfectDayCount']}" if index<len(before) else '—',after[index]['name'] if index<len(after) else '—',f"{after[index]['niceDayCount']} / {after[index]['perfectDayCount']}" if index<len(after) else '—'] for index in range(length)]),'']
    out+=['## Source and model limits','',
    'The common northern GOES TMY choice improves sunny-share and direct-radiation errors at Lima, San Francisco and San Diego against independently integrated 2022/2024 full-disc data. TDY failed that common control comparison at San Diego. Iquique improves too, but retains 15.64 percentage points of monthly sunshine error against those two years. Antofagasta and Valparaíso use the two full-disc years themselves; reproducing their monthly means proves the calculation, not held-out climatology accuracy. The approximately 4 km typical-year and 2 km full-disc pixels can resolve coastal fog differently. [Detailed controls and limitations](NAMED_CONTROL_CHECKS.md).','',
    'The model keeps a native zero-radiation sample at zero heating. A monthly multiplicative correction cannot create unsupported native sunlight. The final fourteen-place build records 27 month/hour combinations with this limitation; the largest omitted daytime heating bound is 0.24 °C.','',
    'The actual fitted fourteen-place coefficient packets also pass JavaScript/Go parity across twelve months, four seasons, November–March and the year, before and after binary packet transport: 15,624 numeric comparisons differ by at most 2.85e−14; displayed nice/perfect/awful counts agree. Air high, evening, dew point, wet-day fraction and wind remain unchanged. SSD evidence: `sun/named-validation/final-fourteen-model-parity/`.','',
    '## Retained evidence','',f'- Exact release comparison: `{args.comparison}`.',f'- Satellite input targets: `{args.targets}`.']
    if args.controls:out+=[f'- Full-period source validation: `{args.controls}`.']
    out+=['- Baseline live API and headless WebKit screenshots: SSD `sun/named-validation/before-api/` and `before-webkit/`.','- The cold baseline laptop year headline read “Chiclayo, Chimbote and Najran”; its November–March headline included Antofagasta. Phone headlines rank the smaller visible map extent.','']
    args.output.write_text('\n'.join(out)+'\n')

if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--comparison',type=Path,required=True);parser.add_argument('--targets',type=Path,required=True);parser.add_argument('--controls',type=Path)
    parser.add_argument('--status',default='Local validation only; not published.');parser.add_argument('--output',type=Path,required=True)
    main(parser.parse_args())
