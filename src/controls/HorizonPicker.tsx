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
          {horizon}
        </button>
      ))}
    </div>
  );
}
