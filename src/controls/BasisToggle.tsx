import type { Basis } from '../data/types';

type Props = {
  value: Basis;
  onChange: (basis: Basis) => void;
  enabled: Basis[];
};

export function BasisToggle({ value, onChange, enabled }: Props) {
  return (
    <div className="segmented" aria-label="Basis">
      {(['forecast', 'climatology'] as const).map((basis) => (
        <button
          key={basis}
          className={basis === value ? 'active' : ''}
          disabled={!enabled.includes(basis)}
          onClick={() => onChange(basis)}
        >
          {basis}
        </button>
      ))}
    </div>
  );
}
