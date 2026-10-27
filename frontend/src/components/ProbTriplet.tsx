import { formatPct } from '@/lib/format';

type Props = {
  pHome: number;
  pDraw: number;
  pAway: number;
  // Short labels ("Home") in dense lists, long ("Home win") on cards.
  long?: boolean;
};

export function ProbTriplet({ pHome, pDraw, pAway, long = false }: Props) {
  const cells = [
    ['home', long ? 'Home win' : 'Home', pHome],
    ['draw', 'Draw', pDraw],
    ['away', long ? 'Away win' : 'Away', pAway],
  ] as const;
  return (
    <div className="prob-triplet">
      {cells.map(([side, label, p]) => (
        <div key={side} className={`prob prob--${side}`}>
          <span className="prob__label">{label}</span>
          <span className="prob__value">{formatPct(p)}</span>
        </div>
      ))}
    </div>
  );
}
