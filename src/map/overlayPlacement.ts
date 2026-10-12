export function scoreOverlayBefore(layers: Array<{ id: string; type: string }>): string | undefined {
  return layers.find((layer) => layer.type === 'line' && /boundary|border|admin/i.test(layer.id))?.id
    ?? layers.find((layer) => layer.type === 'symbol')?.id;
}
