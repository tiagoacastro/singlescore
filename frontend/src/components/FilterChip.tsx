import type { CSSProperties } from 'react';

type Props = {
  label: string;
  count: number;
  active: boolean;
  color: string;
  onClick: () => void;
};

// Toggle chip with a count badge, tinted with `color` when active.
export function FilterChip({ label, count, active, color, onClick }: Props) {
  return (
    <button
      type="button"
      className={`filter-chip${active ? ' filter-chip--active' : ''}`}
      style={{ '--chip': color } as CSSProperties}
      aria-pressed={active}
      onClick={onClick}
    >
      <span>{label}</span>
      <span className="filter-chip__count">{count}</span>
    </button>
  );
}
