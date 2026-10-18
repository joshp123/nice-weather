// Written by AI on 2026.10.18 by model GPT-6.1 Sol.
// This function is also embedded in outlook pages. Keep it self-contained.
export async function shareImage(image, text, filename) {
  const file = new File([image], filename, { type: 'image/png' });
  if (typeof navigator.share === 'function' && navigator.canShare?.({ files: [file] })) {
    await navigator.share({ files: [file], text });
    return 'shared';
  }
  await navigator.clipboard.write([new ClipboardItem({
    'image/png': image,
    'text/plain': new Blob([text], { type: 'text/plain' }),
  })]);
  return 'copied';
}
