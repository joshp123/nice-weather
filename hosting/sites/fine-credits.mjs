// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// The active data generation supplies the method page's fine-data credit link.
const escapeHtml = value => String(value).replace(/[&<>"']/g, character => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[character]));

export function fineMethodHtml(html, configuration) {
  const attribution = `/data/${configuration.attribution}`;
  const detail = `<li><b>Detail.</b> ${configuration.defaultTasteOnly ? 'At the default taste, supported named places' : 'Named places'} in the typical year use their containing 30 arc-second temperature pixel, about 1 km north to south. Monthly high and low anchors come from PRISM in the contiguous United States, then WorldClim 1.4, CHELSA and native ERA5 where needed. ERA5 adjusts each source’s climate period to the actual sampled dates. Daytime temperatures use separate high and low corrections; the 21:00 correction is their average. Every place retains its own native ERA5 weather point, with no inland borrowing or sea-level shift. Dew point, wind, rain and radiation retain ERA5’s coarser resolution. The overview map and forecast keep their existing resolution.${configuration.defaultTasteOnly ? ' Unnamed points and changed tastes use the existing 25 km model.' : ''}</li>`;
  const limits = '<li><b>Local weather remains uncertain.</b> The typical year’s named places have fine temperature detail, but wind, humidity, rain and radiation remain coarse. A 1 km source pixel does not establish 1 km observational accuracy. The overview map can differ from a place’s corrected numbers. Rotterdam in May is a known score regression in the station-temperature diagnostic.</li>';
  const credits = `<p><b>Fine temperature data: PRISM Group, Oregon State University</b>, <a href="https://prism.oregonstate.edu/">prism.oregonstate.edu</a>, 1991–2020 monthly normals, accessed October 2026. Modified and combined; these are not original PRISM maps. Outside the contiguous United States: <a href="https://www.worldclim.org/data/v1.4/worldclim14.html">WorldClim 1.4</a> (Hijmans et al., 2005; CC BY-SA 4.0), with <a href="https://www.chelsa-climate.org/datasets/chelsa_climatologies">CHELSA 2.1</a> (Karger et al.; CC0) and ERA5 fallbacks.</p>
<p>Temperature corrections are available under <a href="https://creativecommons.org/licenses/by-sa/4.0/">CC BY-SA 4.0</a>. Native weather is derived from ECMWF/Copernicus ERA5, via Earthmover, under CC BY 4.0. Contains modified Copernicus Climate Change Service information (2026); neither the European Commission nor ECMWF is responsible for its use. Coastline and timezone properties are separately offered under ODbL. <a href="${escapeHtml(attribution)}">Full data attribution, modifications and licences</a>.</p>`;
  return html
    .replace(/<li><b>Detail\.<\/b>[\s\S]*?<\/li>/, detail)
    .replace(/<li><b>A 25 km grid misses microclimates\.<\/b>[\s\S]*?<\/li>/, limits)
    .replace(/(<h2 id="credits">Credits<\/h2>)/, `$1\n${credits}`);
}
