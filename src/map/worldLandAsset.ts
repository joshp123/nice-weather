export const WORLD_LAND_ASSET = Object.freeze({
  url: '/world-land.png',
  sha256: '22c67093399b55ccb72fe633797f2a58640de83d046f38d25ff0134195acc1a1',
  width: 2048,
  height: 2048,
  projection: 'EPSG:3857',
  bounds: [-20037508.3427892, -20037508.3427892, 20037508.3427892, 20037508.3427892] as const,
  source: Object.freeze({
    dataset: 'Natural Earth 5.1.1 ne_10m_land',
    archiveSha256: 'e547d749445eaa0964aba76738090ec88f5e63c4585122170f98c67a7ea922dc',
    licence: 'public domain',
  }),
  generation: 'Reproject the pinned shapefile to EPSG:3857; rasterise land=255 and water=0 over the declared bounds at 2048x2048; encode as a level-9 PNG; make water transparent and land #f4f2ec.',
});
