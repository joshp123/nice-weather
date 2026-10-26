// Written by AI on 2026.10.19 by model GPT-6.1 Sol.
// Backport of MapLibre's BSD-licensed fix: https://github.com/maplibre/maplibre-gl-js/pull/7765
export function maplibreParseStatePatch() {
  return {
    name: 'maplibre-5.24-interrupted-tile-reload',
    enforce: 'pre',
    transform(source, id) {
      if (!id.endsWith('/maplibre-gl/dist/maplibre-gl.js')) return;
      const consumeState = 'consumeParsing(e){const t=this.parsing[e];if(t)return delete this.parsing[e],t}';
      const reload = 'const s=this.tileState.consumeParsing(e);return yield this._parseWorkerTile(i,t,s)';
      if (!source.includes(consumeState) || source.split(reload).length !== 3) {
        throw new Error('The MapLibre 5.24 backport no longer matches; remove it when upgrading to a version with upstream fix #7765.');
      }
      return {
        code: source.replace(consumeState, 'getParsing(e){return this.parsing[e]}')
          .replaceAll(reload, 'const s=this.tileState.getParsing(e);try{return yield this._parseWorkerTile(i,t,s)}finally{this.tileState.removeParsing(e)}')
          .replaceAll('clearParsing(', 'removeParsing('),
        map: null,
      };
    },
  };
}
