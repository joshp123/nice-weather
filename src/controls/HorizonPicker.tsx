type Props = {
  horizons: string[];
  value: string | undefined;
  onChange: (horizon: string) => void;
};

export function HorizonPicker({ horizons, value, onChange }: Props) {
  return (
    <div className="horizonGrid" aria-label="Horizon">
      {horizons.map((horizon) => (
        <button key={horizon} className={horizon === value ? 'active' : ''} onClick={() => onChange(horizon)}>
          {horizonLabel(horizon)}
        </button>
      ))}
    </div>
  );
}

function horizonLabel(horizon: string) {
  if (horizon === 'd1') return 'Daily forecast';
  return horizon;
}
