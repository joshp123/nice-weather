// Written by AI on 2026.10.26 by model Claude Fable 5.1.
// The interface under trial. During the October 2026 design audit, several candidate interfaces live in one build;
// ?design=<name> picks one, and the flag stays in every link so a candidate can be opened, shared and judged.
// 'current' is the interface the live site shows. Each candidate switches its own part of the interface on this name.
export type DesignName = 'current' | 'ledger' | 'calendar' | 'whymap' | 'hop';

const DESIGN_NAMES: readonly DesignName[] = ['current', 'ledger', 'calendar', 'whymap', 'hop'];

function readDesign(): DesignName {
  const requested = new URL(location.href).searchParams.get('design');
  return DESIGN_NAMES.find(name => name === requested) ?? 'current';
}

export const DESIGN: DesignName = readDesign();

/** The same link, carrying the design flag when one is under trial. */
export function withDesign(link: string): string {
  if (DESIGN === 'current') return link;
  const url = new URL(link, location.origin);
  url.searchParams.set('design', DESIGN);
  return url.pathname + url.search;
}
