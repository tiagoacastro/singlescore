import type { Provenance } from '@/graphql/types';

interface ProvenanceChipProps {
  provenance: Provenance;
  label?: string;
}

const PROVENANCE_LABELS: Record<Provenance, string> = {
  LIVE: 'Live',
  DEMO: 'Demo',
  REPLAY: 'Replay',
  HISTORICAL: 'Historical',
};

const PROVENANCE_CLASS: Record<Provenance, string> = {
  LIVE: 'chip chip--live',
  DEMO: 'chip chip--demo',
  REPLAY: 'chip chip--replay',
  HISTORICAL: 'chip chip--historical',
};

export function ProvenanceChip({ provenance, label }: ProvenanceChipProps) {
  return (
    <span className={PROVENANCE_CLASS[provenance]} title={`Data provenance: ${provenance}`}>
      {label ?? PROVENANCE_LABELS[provenance]}
    </span>
  );
}

interface StatusChipProps {
  status: string;
  minute?: number | null;
}

export function MatchStatusChip({ status, minute }: StatusChipProps) {
  if (status === 'LIVE') {
    return <span className="chip chip--live">● {minute ?? 0}′</span>;
  }
  if (status === 'HALFTIME') {
    return <span className="chip chip--live">HT</span>;
  }
  if (status === 'FINISHED') {
    return <span className="chip chip--historical">FT</span>;
  }
  return <span className="chip">{status.toLowerCase()}</span>;
}
