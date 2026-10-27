import type { Provenance } from '@/graphql/types';

interface ProvenanceChipProps {
  provenance: Provenance;
  label?: string;
}

const PROVENANCE_LABELS: Record<Provenance, string> = {
  DEMO: 'Demo',
  REPLAY: 'Replay',
  HISTORICAL: 'Historical',
};

const PROVENANCE_CLASS: Record<Provenance, string> = {
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
}

export function MatchStatusChip({ status }: StatusChipProps) {
  if (status === 'FINISHED') {
    return <span className="chip chip--historical">FT</span>;
  }
  return <span className="chip">{status.toLowerCase()}</span>;
}
