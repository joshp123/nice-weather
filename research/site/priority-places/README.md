# Rank places in view, then label the winners

Written by AI on 2026.10.18 by model GPT-6.1 Sol.

Headlines and the list use the existing outlook-style ranking function over recognisable places in the unobscured map area, independent of which labels MapLibre happened to place. The population floor is 300,000 below zoom 3, 50,000 below zoom 5, and no floor closer in. The rule keeps choices an eighth of the visible map width apart, measured along its middle latitude and clamped to 30–250 km and names a winning location after the largest eligible place within 50 km with a similar score. Ranking favours nice days, perfect days, typical-day score, then population.

The first three ranked places always enter a priority label layer, regardless of the ordinary city-label population floor or collision placement. Their pills use the same engine counts as the list and card. Nearby priority labels are staggered; a point and pointer retain the place's actual location. Headline and map labels both open the place card. Opening the list retains the same ranking.

Places with zero nice days are excluded. An empty ranking says “No nice days on this map” with the selected stretch, rather than calling zero-day places nicest. The list repeats the same message.

`check.py` runs the real local app in headless Chromium at 390 × 844 and 1512 × 982. It checks November to March, the 14-day forecast, a closer Spain view, and an empty January view over Norway. Each headline place must be a rendered priority label with a positive day count; that number must equal the place API's card count. The opened list must start with the same names in the same order. The script changes the browser response only to expose the existing map for read-only assertions.

Screenshots and live report: `/tmp/nwf-agents/round5/priority-places/`. Build passes. No scoring, day-count, Worker or data-pipeline changes were needed for this fix. Nothing was deployed or pushed.

Final run: eight views passed, including two empty views; all 16 headline labels matched the card counts, all opened lists matched the headline order, and no browser errors occurred.

View-dependent spacing follow-up: `spacing-check.py` captures November to March at world zoom and over Spain at both sizes. The app passes the distance into the existing ranking function. The outlook has a separate Go implementation and fixed World, Europe and USA views; all three main view widths divided by eight exceed 250 km, so its 250 km rule remains unchanged.

Spacing results: Spain uses 154 km on laptop and 40 km on phone. The laptop headline now selects Gandia (79), Seville (60) and Lorca (60), replacing Setúbal (41). Both world views keep 250 km and their previous headlines. All 12 headline label counts matched their card counts; list order matched in all four views, with no browser errors. Captures: `/tmp/nwf-agents/round5/view-spacing/`.
