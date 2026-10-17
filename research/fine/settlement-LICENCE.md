Written by AI on 2026.10.17 by model GPT-6.1 Sol.

# Population weighting source

GHSL GHS-POP R2023A, epoch 2020, WGS84 30 arcseconds, V1-0 is the
single population input. Its global ZIP is 482,351,880 bytes and can be
downloaded anonymously. No account, key or paid service is required.

The [primary GHSL use conditions](https://human-settlement.emergency.copernicus.eu/GHSLhowToCite.php)
explicitly grant CC BY 4.0. The decisive short excerpt is: “copy, redistribute,
adapt, and build upon the material for any purpose, including commercial uses”.
The source must be credited, changes identified, and endorsement avoided.
The same page requires both the release methods paper and the specific dataset
citation; a generic link to GHSL does not satisfy its citation guidance.
These conditions permit public derived weather values with source attribution.
CC BY 4.0 population weights can contribute to the weather's CC BY-SA 4.0
adaptation; they impose no additional share-alike restriction.

The [primary dataset record](https://data.jrc.ec.europa.eu/dataset/2ff68a52-5b5b-4a22-8f40-c41da8332cfe)
confirms anonymous access, the 30 arcsecond WGS84 grid, and the units: people per
pixel, not people per square kilometre. It describes administrative/census
population totals disaggregated using GHSL residential built-up information.
This is modelled resident population for 2020, not a measure of tourists or
climate observations. Do not multiply populated weights by pixel area again.
Zero is a valid unpopulated pixel; missing source data remains missing.
The actual downloaded TIFF has no nodata tag. A complete scan of all
923,831,568 pixels found only finite, nonnegative counts (total population
7,840,952,947); all 881,359,774 zero pixels remain zero. The producer's actual
affine coordinates are retained: the extent is approximately ±89.1° latitude
and extends slightly beyond ±180° longitude. Queries outside that latitude
extent return missing; longitudes wrap at the dateline. No population is
invented for the polar caps.

The downloaded source release remains R2023A/V1-0. The current catalogue's
requested citation uses 2026 and lists both the newer and original DOI. The
acquisition script records that citation without relabelling the source version.

`settlement_sources.py` caches the ZIP, verifies its extracted TIFF's ZIP CRC,
records source and TIFF SHA256 hashes, and archives the actual licence, product,
catalogue and CC BY 4.0 legal-text pages in `fine-sources/settlement/evidence`.
It writes `dataset.json` and the complete publication snippet `ATTRIBUTION.txt`
beside the source. The source sampler does not fit or use weather stations.

Acquisition: `UV_CACHE_DIR=/tmp/uv-cache devenv shell -- uv run --python 3.12 --with numpy --with rasterio --with tifffile --with imagecodecs python research/fine/settlement_sources.py`

Source archive:
<https://jeodpp.jrc.ec.europa.eu/ftp/jrc-opendata/GHSL/GHS_POP_GLOBE_R2023A/GHS_POP_E2020_GLOBE_R2023A_4326_30ss/V1-0/GHS_POP_E2020_GLOBE_R2023A_4326_30ss_V1_0.zip>

Required attribution:

© European Union, 1995-2025. European Commission, Joint Research Centre.
GHSL GHS-POP R2023A, epoch 2020, WGS84 30 arcsecond grid, V1-0.
Licensed under [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/).

Schiavina, M., Freire, S., Carioli, A., MacManus, K. (2026). GHS-POP R2023A —
GHS population grid multitemporal (1975-2030). European Commission, Joint
Research Centre [Dataset]. DOI: [10.2905/JRC.CXKEDRR](https://doi.org/10.2905/JRC.CXKEDRR);
[10.2905/2FF68A52-5B5B-4A22-8F40-C41DA8332CFE](https://doi.org/10.2905/2FF68A52-5B5B-4A22-8F40-C41DA8332CFE).

Pesaresi et al. (2024). Advances on the Global Human Settlement Layer by joint
assessment of Earth Observation and population survey data. International Journal
of Digital Earth, 17(1). DOI: [10.1080/17538947.2024.2390454](https://doi.org/10.1080/17538947.2024.2390454).

Changes: source population counts select the relative weights of independent
fine-grid climate anchors inside each quarter-degree footprint. The resulting
weather values are derived calculations, not an official GHSL product.
No endorsement by the European Union or the source authors is implied.
