import type { Profile } from '../data/types';

type Props = {
  value: Profile;
  onChange: (profile: Profile) => void;
};

export function ProfileToggle({ value, onChange }: Props) {
  return (
    <div className="segmented" aria-label="Profile">
      {(['general', 'cycling'] as const).map((profile) => (
        <button key={profile} className={profile === value ? 'active' : ''} onClick={() => onChange(profile)}>
          {profile}
        </button>
      ))}
    </div>
  );
}

