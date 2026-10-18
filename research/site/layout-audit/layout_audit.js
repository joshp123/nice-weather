// Written by AI on 2026.10.18 by model claude opus 5.5.
// Lists every visible interface element (text, controls, panels) with its type and colour, every pair of
// visible elements that overlap, and every alignment fault. Run in the page; check_layout.py drives it.
() => {
  const clippedRect = element => {
    let { left, top, right, bottom } = element.getBoundingClientRect();
    for (let node = element.parentElement; node && node !== document.body; node = node.parentElement) {
      const style = getComputedStyle(node);
      if (style.overflowX === 'visible' && style.overflowY === 'visible') continue;
      const clip = node.getBoundingClientRect();
      left = Math.max(left, clip.left); top = Math.max(top, clip.top); right = Math.min(right, clip.right); bottom = Math.min(bottom, clip.bottom);
    }
    return { left, top, right, bottom, width: Math.max(0, right - left), height: Math.max(0, bottom - top) };
  };
  // A modal (the poster) hides the page behind it: only its own contents count.
  const modal = document.querySelector('.poster-overlay');
  const visible = element => {
    if (modal && !modal.contains(element) && element !== modal) return false;
    const style = getComputedStyle(element);
    if (style.visibility === 'hidden' || style.display === 'none' || Number(style.opacity) === 0) return false;
    for (let node = element; node; node = node.parentElement) {
      const s = getComputedStyle(node);
      if (s.display === 'none' || Number(s.opacity) === 0) return false;
    }
    const rect = clippedRect(element);
    return rect.width > 1 && rect.height > 1 && rect.right > 0 && rect.bottom > 0 && rect.left < innerWidth && rect.top < innerHeight;
  };
  const parse = colour => {
    const match = colour.match(/rgba?\(([^)]+)\)/);
    if (!match) return null;
    const parts = match[1].split(/[ ,/]+/).filter(Boolean).map(Number);
    return { r: parts[0], g: parts[1], b: parts[2], a: parts.length > 3 ? parts[3] : 1 };
  };
  const over = (top, bottom) => ({ r: top.r * top.a + bottom.r * (1 - top.a), g: top.g * top.a + bottom.g * (1 - top.a), b: top.b * top.a + bottom.b * (1 - top.a), a: 1 });
  const luminance = c => {
    const channel = v => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; };
    return 0.2126 * channel(c.r) + 0.7152 * channel(c.g) + 0.0722 * channel(c.b);
  };
  const contrast = (a, b) => { const x = luminance(a), y = luminance(b); return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05); };
  const backdrop = element => {
    const layers = [];
    for (let node = element; node && node !== document.documentElement; node = node.parentElement) {
      const background = parse(getComputedStyle(node).backgroundColor);
      if (background && background.a > 0) layers.push(background);
      if (background && background.a >= 0.99) break;
      if (node.classList && node.classList.contains('maplibregl-map')) return { overMap: true, layers };
    }
    if (!layers.length || layers[layers.length - 1].a < 0.99) return { overMap: true, layers };
    let colour = layers[layers.length - 1];
    for (let index = layers.length - 2; index >= 0; index--) colour = over(layers[index], colour);
    return { overMap: false, colour };
  };
  const describe = element => {
    const name = element.tagName.toLowerCase() + (element.className && typeof element.className === 'string' ? '.' + element.className.trim().split(/\s+/).join('.') : '');
    return name.slice(0, 80);
  };
  const box = rect => ({ x: Math.round(rect.left), y: Math.round(rect.top), w: Math.round(rect.width), h: Math.round(rect.height) });
  const scrollingPage = document.scrollingElement.scrollHeight > innerHeight + 2;
  const intersect = (a, b) => {
    const x = Math.min(a.x + a.w, b.x + b.w) - Math.max(a.x, b.x);
    const y = Math.min(a.y + a.h, b.y + b.h) - Math.max(a.y, b.y);
    return x > 1 && y > 1 ? x * y : 0;
  };

  const all = [...document.querySelectorAll('body *')].filter(element => !(element instanceof SVGElement && element.ownerSVGElement) && element.tagName !== 'CANVAS' && element.tagName !== 'SCRIPT' && element.tagName !== 'STYLE');
  const shown = all.filter(visible);

  // Text: the boxes of the actual glyph runs, so text drawn over other text is caught.
  const texts = [];
  for (const element of shown) {
    const own = [...element.childNodes].filter(node => node.nodeType === 3 && node.textContent.trim());
    if (!own.length) continue;
    const range = document.createRange();
    const rects = [];
    for (const node of own) { range.selectNodeContents(node); rects.push(...[...range.getClientRects()].filter(r => r.width > 0.5 && r.height > 0.5)); }
    const clip = clippedRect(element);
    for (let index = rects.length - 1; index >= 0; index--) {
      const r = rects[index];
      const left = Math.max(r.left, clip.left), top = Math.max(r.top, clip.top), right = Math.min(r.right, clip.right), bottom = Math.min(r.bottom, clip.bottom);
      if (right - left < 1 || bottom - top < 1) rects.splice(index, 1);
      else rects[index] = new DOMRect(left, top, right - left, bottom - top);
    }
    if (!rects.length) continue;
    const style = getComputedStyle(element);
    const back = backdrop(element);
    const colour = parse(style.color);
    texts.push({
      element, name: describe(element), text: own.map(node => node.textContent.trim()).join(' ').slice(0, 90),
      rects: rects.map(box), box: box(element.getBoundingClientRect()),
      fontSize: parseFloat(style.fontSize), fontWeight: style.fontWeight, fontFamily: style.fontFamily.split(',')[0].replace(/['"]/g, ''),
      colour: style.color, overMap: back.overMap,
      contrast: back.overMap ? null : Math.round(contrast(colour.a < 1 ? over(colour, back.colour) : colour, back.colour) * 100) / 100,
      background: back.overMap ? 'map' : `rgb(${Math.round(back.colour.r)}, ${Math.round(back.colour.g)}, ${Math.round(back.colour.b)})`,
    });
  }

  // Surfaces: anything that floats over the map (panels, buttons, controls).
  const surfaces = shown.filter(element => {
    if (element === modal) return false;
    const style = getComputedStyle(element);
    if (element.classList.contains('maplibregl-map') || element.classList.contains('maplibregl-canvas-container') || element.classList.contains('maplibregl-control-container')) return false;
    if (element.matches('.maplibregl-ctrl-top-left,.maplibregl-ctrl-top-right,.maplibregl-ctrl-bottom-left,.maplibregl-ctrl-bottom-right')) return false;
    const background = parse(style.backgroundColor);
    const painted = (background && background.a > 0.05) || style.boxShadow !== 'none' || style.backgroundImage !== 'none';
    // An unpainted, absolutely placed button is a hit area over an image (the poster's place names), not a surface.
    if (!painted && style.position === 'absolute') return false;
    return painted || element.matches('button, a, input, summary');
  }).map(element => ({ element, name: describe(element), box: box(clippedRect(element)) }));

  const overlaps = [];
  const related = (a, b) => a.contains(b) || b.contains(a);
  for (let i = 0; i < surfaces.length; i++) for (let j = i + 1; j < surfaces.length; j++) {
    const a = surfaces[i], b = surfaces[j];
    if (related(a.element, b.element)) continue;
    const area = intersect(a.box, b.box);
    if (area > 4) overlaps.push({ kind: 'surface', a: a.name, b: b.name, area });
  }
  for (let i = 0; i < texts.length; i++) for (let j = i + 1; j < texts.length; j++) {
    const a = texts[i], b = texts[j];
    if (related(a.element, b.element)) continue;
    let area = 0;
    for (const r of a.rects) for (const s of b.rects) area += intersect(r, s);
    if (area > 4) overlaps.push({ kind: 'text', a: `${a.name} "${a.text.slice(0, 30)}"`, b: `${b.name} "${b.text.slice(0, 30)}"`, area });
  }
  // Text that runs out of the window.
  for (const t of texts) for (const r of t.rects) if (r.x < -1 || r.x + r.w > innerWidth + 1 || (!scrollingPage && (r.y < -1 || r.y + r.h > innerHeight + 1))) { overlaps.push({ kind: 'clipped', a: `${t.name} "${t.text.slice(0, 30)}"`, b: 'window edge', area: 0 }); break; }

  // Map labels: the boxes MapLibre placed, when the app exposes its map.
  const map = window.niceWeatherMap;
  const mapLabels = [];
  if (map) {
    const features = map.queryRenderedFeatures({ layers: map.getStyle().layers.filter(layer => layer.type === 'symbol').map(layer => layer.id) });
    for (const feature of features) mapLabels.push({ layer: feature.layer.id, name: feature.properties.name || feature.properties['name:en'] || '' });
    const placed = [];
    const grid = map.style?.placement?.collisionIndex?.grid;
    if (grid) {
      const tokens = getComputedStyle(document.documentElement); const pad = 100; const shrink = Number(tokens.getPropertyValue('--map-label-padding')) * (Number(tokens.getPropertyValue('--map-label-scale')) || 1);
      const canvas = map.getCanvas().getBoundingClientRect();
      for (let index = 0; index < grid.boxKeys.length; index++) {
        const [x1, y1, x2, y2] = [0, 1, 2, 3].map(offset => grid.bboxes[index * 4 + offset]);
        const box = { x: canvas.left + x1 - pad + shrink, y: canvas.top + y1 - pad + shrink, w: x2 - x1 - 2 * shrink, h: y2 - y1 - 2 * shrink };
        if (box.w > 0 && box.h > 0) placed.push({ name: `label ${index}`, box });
      }
    }
    for (let i = 0; i < placed.length; i++) for (let j = i + 1; j < placed.length; j++) {
      const area = intersect(placed[i].box, placed[j].box);
      if (area > 4) overlaps.push({ kind: 'map-label', a: placed[i].name, b: placed[j].name, area });
    }
    for (const placedLabel of placed) for (const bar of document.querySelectorAll('.top-bar, .bottom-bar')) {
      const area = intersect(placedLabel.box, box(bar.getBoundingClientRect()));
      if (area > 4) overlaps.push({ kind: 'label-under-bar', a: placedLabel.name, b: describe(bar), area });
    }
    mapLabels.length = placed.length;
  }

  // Alignment: one control height; rows share edges and baselines; columns share the outer margin.
  const alignment = [];
  const root = getComputedStyle(document.documentElement);
  const px = name => { const probe = document.createElement('div'); probe.style.position = 'absolute'; probe.style.width = `var(${name})`; document.body.append(probe); const value = probe.getBoundingClientRect().width; probe.remove(); return value; };
  const control = px('--control'), margin = px('--margin');
  const shownSet = new Set(shown);
  const rectOf = element => element.getBoundingClientRect();
  const label = element => describe(element) + (element.textContent ? ` "${element.textContent.trim().slice(0, 24)}"` : '');
  const near = (a, b) => Math.abs(a - b) <= 1;
  if (control > 0) {
    for (const element of document.querySelectorAll('button.button, button.icon-button, button.chip, .search, .segmented')) {
      if (!shownSet.has(element)) continue;
      if (element.closest('.search') && element !== element.closest('.search')) continue;
      const height = rectOf(element).height;
      if (!near(height, control)) alignment.push({ check: 'control height', element: label(element), got: Math.round(height * 10) / 10, want: control });
    }
  }
  const rowCheck = (name, container, children) => {
    if (!container || !shownSet.has(container)) return;
    const items = children.filter(element => element && shownSet.has(element));
    if (items.length < 2) return;
    const centres = items.map(element => { const r = rectOf(element); return r.top + r.height / 2; });
    if (Math.max(...centres) - Math.min(...centres) > 1) alignment.push({ check: `${name}: shared centre line`, items: items.map(label), centres: centres.map(Math.round) });
  };
  const topBar = document.querySelector('.top-bar'), bottomBar = document.querySelector('.bottom-bar');
  const phone = innerWidth <= 720;
  if (topBar) rowCheck('top bar', topBar, [...topBar.children, ...(document.querySelector('.top-actions')?.children ?? [])].filter(element => !element.classList.contains('top-actions') && !(phone && element.classList.contains('search'))));
  if (!phone && bottomBar) rowCheck('bottom bar', bottomBar, [document.querySelector('.time-button'), document.querySelector('.bar-note'), document.querySelector('.legend-scale'), document.querySelector('.legend-caption'), document.querySelector('.small-print')]);
  if (phone && bottomBar) rowCheck('phone bar row', document.querySelector('.bar-row'), [...(document.querySelector('.bar-row')?.children ?? [])]);
  for (const header of document.querySelectorAll('.panel-header')) rowCheck('panel header buttons', header, [...header.querySelectorAll(':scope > .icon-button, :scope > .panel-actions > *')]);
  // Same-size text in a row sits on one baseline.
  const baselineRow = (name, container) => {
    if (!container || !shownSet.has(container)) return;
    const groups = {};
    for (const t of texts) if (container.contains(t.element) && !t.element.closest('.search-results')) (groups[t.fontSize] ??= []).push(t);
    for (const [size, group] of Object.entries(groups)) {
      if (group.length < 2) continue;
      const bottoms = group.map(t => t.rects[0].y + t.rects[0].h);
      if (Math.max(...bottoms) - Math.min(...bottoms) > 1) alignment.push({ check: `${name}: ${size}px text on one baseline`, items: group.map(t => t.text.slice(0, 20)), bottoms: bottoms.map(Math.round) });
    }
  };
  if (!phone) { baselineRow('top tools', document.querySelector('.top-actions')); baselineRow('bottom bar left', document.querySelector('.bar-start')); baselineRow('bottom bar right', document.querySelector('.bar-end')); }
  baselineRow('answer context', document.querySelector('.answer-context'));
  // Columns: the outer margin on both sides.
  const leftColumn = phone ? [document.querySelector('.wordmark'), document.querySelector('.bar-row')] : [document.querySelector('.wordmark'), document.querySelector('.time-button'), document.querySelector('.slot-list > .panel'), document.querySelector('.slot-picker > .panel')];
  for (const element of leftColumn) if (element && shownSet.has(element) && !near(rectOf(element).left, margin)) alignment.push({ check: 'left margin', element: label(element), got: Math.round(rectOf(element).left), want: margin });
  const rightColumn = phone ? [document.querySelector('.top-actions'), document.querySelector('.bar-row')] : [document.querySelector('.top-actions'), document.querySelector('.bar-end'), document.querySelector('.slot-card > .panel'), document.querySelector('.slot-taste > .panel')];
  for (const element of rightColumn) if (element && shownSet.has(element) && !near(innerWidth - rectOf(element).right, margin)) alignment.push({ check: 'right margin', element: label(element), got: Math.round(innerWidth - rectOf(element).right), want: margin });
  // Inside a panel, every block starts at the panel's padding.
  for (const panel of document.querySelectorAll('.card, .taste, .place-list, .time-picker-panel')) {
    if (!shownSet.has(panel)) continue;
    const inner = rectOf(panel).left + parseFloat(getComputedStyle(panel).paddingLeft) + parseFloat(getComputedStyle(panel).borderLeftWidth);
    for (const child of panel.children) if (shownSet.has(child) && !near(rectOf(child).left, inner)) alignment.push({ check: 'panel left edge', element: label(child), got: Math.round(rectOf(child).left), want: Math.round(inner) });
  }

  const typeScale = {};
  for (const t of texts) { const key = `${t.fontFamily} ${t.fontSize}px ${t.fontWeight}`; typeScale[key] = (typeScale[key] || 0) + 1; }
  const weights = [...new Set(texts.map(t => t.fontWeight))];
  const sizes = [...new Set(texts.map(t => t.fontSize))].sort((a, b) => a - b);
  const lowContrast = texts.filter(t => t.contrast !== null && t.contrast < 4.5).map(t => ({ name: t.name, text: t.text, contrast: t.contrast, size: t.fontSize }));
  return {
    viewport: { width: innerWidth, height: innerHeight },
    texts: texts.map(({ element, ...rest }) => rest),
    surfaces: surfaces.map(({ element, ...rest }) => rest),
    overlaps, alignment, lowContrast, typeScale, weights, sizes, mapLabels: mapLabels.length,
  };
}
