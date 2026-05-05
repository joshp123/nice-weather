import type { ForecastHorizon } from '../data/types';

type Props = {
  horizons: string[];
  details?: ForecastHorizon[];
  value: string | undefined;
  onChange: (horizon: string) => void;
};

export function HorizonPicker({ horizons, details = [], value, onChange }: Props) {
  return (
    <div className="horizonGrid" aria-label="Horizon">
      {horizons.map((horizon) => (
        <button key={horizon} className={horizon === value ? 'active' : ''} onClick={() => onChange(horizon)}>
          {horizonLabel(horizon, details)}
        </button>
      ))}
    </div>
  );
}

function horizonLabel(horizon: string, details: ForecastHorizon[]) {
  const detail = details.find((candidate) => candidate.id === horizon);
  if (detail) return detail.label;
  return horizon;
}
