export async function readBoundedResponse(response: Response, label: string, maximumBytes: number, options: { contentLength?: 'decoded' | 'encoded' } = {}): Promise<Uint8Array<ArrayBuffer>> {
  const contentLength = response.headers.get('Content-Length');
  if (contentLength !== null) {
    if (!/^\d+$/.test(contentLength) || !Number.isSafeInteger(Number(contentLength))) {
      await cancelBody(response.body);
      throw new Error(`Invalid Content-Length for ${label}.`);
    }
    if (options.contentLength !== 'encoded' && Number(contentLength) > maximumBytes) {
      await cancelBody(response.body);
      throw new Error(`${label} is unexpectedly large.`);
    }
  }

  if (response.body === null) {
    const bytes = new Uint8Array(await response.arrayBuffer());
    if (bytes.byteLength > maximumBytes) throw new Error(`${label} is unexpectedly large.`);
    return bytes;
  }

  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let length = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      length += value.byteLength;
      if (length > maximumBytes) {
        try {
          await reader.cancel(`${label} exceeded its byte limit`);
        } catch {
          // Preserve the size error even when transport cancellation fails.
        }
        throw new Error(`${label} is unexpectedly large.`);
      }
      chunks.push(value);
    }
  } finally {
    reader.releaseLock();
  }

  const bytes = new Uint8Array(length);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return bytes;
}

async function cancelBody(body: ReadableStream<Uint8Array> | null): Promise<void> {
  if (body === null) return;
  try {
    await body.cancel('response exceeded its declared byte limit');
  } catch {
    // The bounded read fails regardless; cancellation is best-effort cleanup.
  }
}
