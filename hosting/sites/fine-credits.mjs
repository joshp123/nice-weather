// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// The active data generation supplies the method page's fine-data credit link.
const escapeHtml = value => String(value).replace(/[&<>"']/g, character => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[character]));

export function fineMethodHtml(html, configuration) {
  const attribution = `/data/${configuration.attribution}`;
  const sunshine = configuration.sunshine ? ' Where satellite records cover a named place, monthly sunshine also adjusts the sampled days and the warming felt in direct sun.' : '';
  const detail = `<li><b>Detail.</b> At the default taste, named places in the typical year use temperature data for their own location, with detail of about 1 km. We adjust temperatures from the weather record using local average highs and lows.${sunshine} Rain, wind, humidity and sunshine cover larger areas, so nearby places can share these conditions. The forecast and changed tastes use detail of about 25 km.</li>`;
  const limits = '<li><b>Local weather remains uncertain.</b> Fine temperature detail helps distinguish a town from the hills behind it. It cannot reliably describe a sheltered bay, a foggy street or a single valley. Rain, wind, humidity and sunshine cover larger areas than temperature.</li>';
  const credits = `<p><b>Fine temperature data: PRISM Group, Oregon State University</b>, <a href="https://prism.oregonstate.edu/">prism.oregonstate.edu</a>, 1991–2020 monthly normals, accessed October 2026. Modified and combined; these are not original PRISM maps. Outside the contiguous United States: <a href="https://www.worldclim.org/data/v1.4/worldclim14.html">WorldClim 1.4</a> (Hijmans et al., 2005; CC BY-SA 4.0), with <a href="https://www.chelsa-climate.org/datasets/chelsa_climatologies">CHELSA 2.1</a> (Karger et al.; CC0) and ERA5 fallbacks.</p>
<p>Temperature corrections are available under <a href="https://creativecommons.org/licenses/by-sa/4.0/">CC BY-SA 4.0</a>. Native weather is derived from ECMWF/Copernicus ERA5, via Earthmover, under CC BY 4.0. Contains modified Copernicus Climate Change Service information (2026); neither the European Commission nor ECMWF is responsible for its use. Coastline and timezone properties are separately offered under ODbL. <a href="${escapeHtml(attribution)}">Full data attribution, modifications and licences</a>.</p>${configuration.sunshine ? '\n<p>Sunshine corrections use NSRDB satellite radiation from the National Laboratory of the Rockies (formerly NREL), under <a href="https://creativecommons.org/licenses/by/3.0/us/">CC BY 3.0 US</a>, and <a href="https://re.jrc.ec.europa.eu/pvg_tools/en/">PVGIS</a> / CM SAF SARAH-3. PVGIS © European Communities, 2001–2026. We convert and combine these records into monthly corrections; these are modified values, not original satellite maps. Source periods and coverage are in the full data attribution.</p>' : ''}`;
  return html
    .replace(/<li><b>Detail\.<\/b>[\s\S]*?<\/li>/, detail)
    .replace(/<li><b>A 25 km grid misses microclimates\.<\/b>[\s\S]*?<\/li>/, limits)
    .replace(/(<h2 id="credits">Credits<\/h2>)/, `$1\n${credits}`);
}
