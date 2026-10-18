# Rank places in view, then label the winners

Written by AI on 2026.10.18 by model GPT-6.1 Sol.

Headlines and the list use the existing outlook-style ranking function over recognisable places in the unobscured map area, independent of which labels MapLibre happened to place. The population floor is 300,000 below zoom 3, 50,000 below zoom 5, and no floor closer in. The existing rule keeps choices about 250 km apart and names a winning location after the largest eligible place within 50 km with a similar score. Ranking favours nice days, perfect days, typical-day score, then population.

The first three ranked places always enter a priority label layer, regardless of the ordinary city-label population floor or collision placement. Their pills use the same engine counts as the list and card. Nearby priority labels are staggered; a point and pointer retain the place's actual location. Headline and map labels both open the place card. Opening the list retains the same ranking.

Places with zero nice days are excluded. An empty ranking says “No nice days on this map” with the selected stretch, rather than calling zero-day places nicest. The list repeats the same message.

`check.py` runs the real local app in headless Chromium at 390 × 844 and 1512 × 982. It checks November to March, the 14-day forecast, a closer Spain view, and an empty January view over Norway. Each headline place must be a rendered priority label with a positive day count; that number must equal the place API's card count. The opened list must start with the same names in the same order. The script changes the browser response only to expose the existing map for read-only assertions.

Screenshots and live report: `/tmp/nwf-agents/round5/priority-places/`. Build passes. No scoring, day-count, Worker or data-pipeline changes were needed for this fix. Nothing was deployed or pushed.

Final run: eight views passed, including two empty views; all 16 headline labels matched the card counts, all opened lists matched the headline order, and no browser errors occurred.
