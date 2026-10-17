// Written by AI on 2026.10.17 by model GPT-6.1 Sol.
/**
 * Decodes our 8-bit greyscale PNGs byte for byte. A browser canvas is not used:
 * Safari colour-manages canvas pixels, which turned the sea's 255 into 254 (a
 * valid, maximal score) and shifted every land value.
 */
export async function loadGreyscaleImage(url: string): Promise<Uint8Array> {
  const response = await fetch(url);
  if (!response.ok) throw new Error(`${url}: HTTP ${response.status}`);
  const png = new Uint8Array(await response.arrayBuffer());
  const view = new DataView(png.buffer, png.byteOffset, png.byteLength);
  const width = view.getUint32(16);
  const height = view.getUint32(20);
  if (png[24] !== 8 || png[25] !== 0 || png[28] !== 0) throw new Error(`${url}: not an 8-bit greyscale, non-interlaced PNG`);
  const compressedChunks: Uint8Array[] = [];
  for (let offset = 8; offset < png.length; ) {
    const length = view.getUint32(offset);
    const type = String.fromCharCode(...png.subarray(offset + 4, offset + 8));
    if (type === 'IDAT') compressedChunks.push(png.subarray(offset + 8, offset + 8 + length));
    offset += 12 + length;
  }
  const inflated = new Response(new Blob(compressedChunks as BlobPart[]).stream().pipeThrough(new DecompressionStream('deflate')));
  return unfilterRows(new Uint8Array(await inflated.arrayBuffer()), width, height);
}

/** Reverses PNG row filtering for one byte per pixel (PNG specification, section 9). */
function unfilterRows(filtered: Uint8Array, width: number, height: number): Uint8Array {
  const values = new Uint8Array(width * height);
  for (let row = 0; row < height; row++) {
    const filter = filtered[row * (width + 1)];
    const source = row * (width + 1) + 1;
    const target = row * width;
    for (let column = 0; column < width; column++) {
      const left = column > 0 ? values[target + column - 1] : 0;
      const up = row > 0 ? values[target - width + column] : 0;
      const upLeft = row > 0 && column > 0 ? values[target - width + column - 1] : 0;
      let predictor = 0;
      if (filter === 1) predictor = left;
      else if (filter === 2) predictor = up;
      else if (filter === 3) predictor = (left + up) >> 1;
      else if (filter === 4) {
        const estimate = left + up - upLeft;
        const toLeft = Math.abs(estimate - left), toUp = Math.abs(estimate - up), toUpLeft = Math.abs(estimate - upLeft);
        predictor = toLeft <= toUp && toLeft <= toUpLeft ? left : toUp <= toUpLeft ? up : upLeft;
      }
      values[target + column] = (filtered[source + column] + predictor) & 255;
    }
  }
  return values;
}
