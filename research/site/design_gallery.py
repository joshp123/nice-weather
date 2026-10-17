# Written by AI on 2026.10.18 by model claude opus 5.5. One page with every design attempt, for Josh.
import base64, io, html
from PIL import Image
R = '/tmp/nwf-agents/round5/'; F = '/tmp/nwf-agents/round4/design/build/'
DESIGNS = [
 ('The living poster', 'Opus, wildcard', 'The screen is the poster: a big Nice!, one sentence as the answer with the period inside it, the map framed like Ryan\'s outlook.', 'Judges: first for Sol and Opus, second for Fable. The base of tonight\'s build.',
  [R+'design-w1/shots/first-1512x982-light.png', R+'design-w1/shots/novmar-1512x982-dark.png', R+'design-w1/shots/place-1512x982-light.png', R+'design-w1/shots/poster-1512x982-light.png'], [R+'design-w1/shots/first-390x844-light.png', R+'design-w1/shots/place-390x844-light.png']),
 ('Chase the sun', 'Opus, wildcard', 'Built around your first message: a year dial, your home as the baseline, the best place by van from home, and Play the year.', 'You: "this one has MANY compelling features". Judges: second for Opus, fourth for Fable, fifth for Sol. Its dial, home line, count pills and Play the year are in tonight\'s build.',
  [R+'design-c/shots/novmar-1512x982-light.png', R+'design-c/shots/first-1512x982-light.png', R+'design-c/shots/card-1512x982-light.png', R+'design-c/shots/year-1512x982-light.png'], [R+'design-c/shots/novmar-390x844-light.png', R+'design-c/shots/card-390x844-light.png']),
 ('Map first', 'Opus', 'The map fills the screen; one calm panel holds the period and the answer; the phone works like a native maps app.', 'Judges: first for Fable, second for Sol, third for Opus. Its phone layout and 57-inch layout are in tonight\'s build.',
  [R+'design-a/shots/first-mbp14-light.png', R+'design-a/shots/novmar-mbp14-light.png', R+'design-a/shots/card-mbp14-light.png', R+'design-a/shots/poster-mbp14-light.png'], [R+'design-a/shots/first-phone-light.png', R+'design-a/shots/card-phone-light.png']),
 ('The weather moves', 'Opus, wildcard', 'Press play and the nice weather moves across the world through the 14 days or the year, with a film strip of small maps.', 'Judges: third or fourth. Its play button is in tonight\'s build.',
  [R+'design-b/shots/mbp14-light-1-first.png', R+'design-b/shots/mbp14-light-2-novmar.png', R+'design-b/shots/mbp14-light-3-place.png', R+'design-b/shots/mbp14-light-6-poster.png'], [R+'design-b/shots/phone-light-1-first.png', R+'design-b/shots/phone-light-3-place.png']),
 ('A window on the year', 'Sol, wildcard', 'A sidebar with a calendar wheel of the year; the map beside it.', 'Judges: fourth to sixth: the sidebar takes a quarter of the laptop and the phone map becomes a strip.',
  [R+'design-w2/screenshots/1512-light-first.png', R+'design-w2/screenshots/1512-light-winter.png', R+'design-w2/screenshots/1512-light-place.png', R+'design-w2/screenshots/1512-light-poster.png'], [R+'design-w2/screenshots/390-light-first.png', R+'design-w2/screenshots/390-light-place.png']),
 ('One picker button', 'Fable', 'A bottom bar with one time button that opens a picker; on the phone everything lives in one sheet.', 'Judges: fifth or sixth, "pedestrian": a ranking table leads every screen.',
  [F+'first-mbp14-light.png', F+'winter-mbp14-light.png', F+'card14-mbp14-light.png'], [F+'first-phone-light.png', F+'card14-phone-light.png']),
]
def img(path, width):
    im = Image.open(path).convert('RGB'); h = round(im.height * width / im.width)
    buf = io.BytesIO(); im.resize((width, h), Image.LANCZOS).save(buf, 'JPEG', quality=78, optimize=True)
    return f'<img src="data:image/jpeg;base64,{base64.b64encode(buf.getvalue()).decode()}" style="width:100%;border-radius:8px;display:block">'
parts = ['''<!doctype html><html lang="en-GB"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Nice Weather: the six designs</title>
<style>body{margin:0;font:16px/1.5 -apple-system,BlinkMacSystemFont,sans-serif;color:#1d1d1f;background:#f5f5f7}main{max-width:1500px;margin:0 auto;padding:28px}h1{font-size:26px;margin:0 0 6px}h2{font-size:21px;margin:44px 0 2px}.who{color:#6e6e73;font-size:14px}p{margin:6px 0}.grid{display:grid;grid-template-columns:repeat(2,1fr);gap:14px;margin-top:12px}.phones{display:grid;grid-template-columns:repeat(6,1fr);gap:14px;margin-top:14px}</style></head><body><main>
<h1>The six designs, 18 October</h1>
<p>Six independent attempts, judged cold by three models (Sol, Opus, Fable) from your words only. Tonight's build: the living poster as the base, with chase the sun's year dial, home line, count pills and Play the year, and map first's phone and 57-inch layouts. Full prototypes are kept on the SSD under nice-weather-data/designs/2026-10-18.</p>''']
for name, who, idea, verdict, laptops, phones in DESIGNS:
    parts.append(f'<h2>{html.escape(name)}</h2><div class="who">{html.escape(who)}</div><p>{html.escape(idea)}</p><p><b>{html.escape(verdict)}</b></p>')
    parts.append('<div class="grid">' + ''.join(img(p, 1100) for p in laptops) + '</div>')
    parts.append('<div class="phones">' + ''.join(img(p, 390) for p in phones) + '</div>')
parts.append('</main></body></html>')
open('/Users/josh/code/nice-weather-finder/.agent/designs-2026-10-18.html', 'w').write('\n'.join(parts))
