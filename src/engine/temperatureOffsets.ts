// Written by AI on 2026.10.26 by model GPT-6.
const WIDTH = 1440;
const HEIGHT = 721;

/** Exact nearest measured offset on the wrapped fine grid. The basemap owns the coastline. */
export function nearestTemperatureOffsets(offsets: Uint8Array): Uint8Array {
  const nearestInRow = new Int32Array(offsets.length).fill(-1);
  const rowDistance = new Float64Array(offsets.length).fill(Infinity);
  for (let row = 0; row < HEIGHT; row++) {
    let nearest = -3 * WIDTH;
    for (let column = -WIDTH; column < 2 * WIDTH; column++) {
      const source = row * WIDTH + (column + WIDTH) % WIDTH;
      if (offsets[source] !== 255) nearest = column;
      if (column >= 0 && column < WIDTH && nearest !== -3 * WIDTH) {
        const index = row * WIDTH + column;
        nearestInRow[index] = row * WIDTH + (nearest + WIDTH) % WIDTH;
        rowDistance[index] = (column - nearest) ** 2;
      }
    }
    nearest = 3 * WIDTH;
    for (let column = 2 * WIDTH - 1; column >= -WIDTH; column--) {
      const source = row * WIDTH + (column + WIDTH) % WIDTH;
      if (offsets[source] !== 255) nearest = column;
      if (column >= 0 && column < WIDTH && nearest !== 3 * WIDTH) {
        const index = row * WIDTH + column;
        if ((column - nearest) ** 2 < rowDistance[index]) {
          nearestInRow[index] = row * WIDTH + nearest % WIDTH;
          rowDistance[index] = (column - nearest) ** 2;
        }
      }
    }
  }
  const result = offsets.slice();
  const sourceRows = new Int32Array(HEIGHT);
  const boundaries = new Float64Array(HEIGHT + 1);
  for (let column = 0; column < WIDTH; column++) {
    let last = -1;
    for (let row = 0; row < HEIGHT; row++) {
      const distance = rowDistance[row * WIDTH + column];
      if (!Number.isFinite(distance)) continue;
      let boundary = -Infinity;
      while (last >= 0) {
        const previous = sourceRows[last];
        boundary = (distance + row * row - rowDistance[previous * WIDTH + column] - previous * previous) / (2 * (row - previous));
        if (boundary > boundaries[last]) break;
        last--;
      }
      sourceRows[++last] = row;
      boundaries[last] = last === 0 ? -Infinity : boundary;
      boundaries[last + 1] = Infinity;
    }
    let nearest = 0;
    for (let row = 0; row <= 600; row++) {
      while (boundaries[nearest + 1] < row) nearest++;
      const index = row * WIDTH + column;
      result[index] = offsets[nearestInRow[sourceRows[nearest] * WIDTH + column]];
    }
  }
  result.fill(255, 601 * WIDTH);
  return result;
}
