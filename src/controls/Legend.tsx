import type { LegendCategory } from '../data/types';

type Props = {
  categories: LegendCategory[];
};

export function Legend({ categories }: Props) {
  return (
    <div className="legend" role="group" aria-label="Score color legend">
      {categories.map((category) => (
        <div key={category.label} className="legendItem">
          <span className="swatch" style={{ background: category.color }} />
          <span>{category.label}</span>
          <span className="range">
            {category.min}-{category.max}
          </span>
        </div>
      ))}
    </div>
  );
}
