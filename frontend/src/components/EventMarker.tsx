import { colors, sideColor } from '@/theme';

export type Marker = {
  key: string;
  minute: number; // x position, clamped to the 0-90 axis
  label: string; // minute as shown, e.g. 45'+2
  kind: 'GOAL' | 'RED_CARD';
  teamSide: 'HOME' | 'AWAY' | 'NEUTRAL' | null;
  description: string;
  // Derived from a scoreline step rather than a per-event feed; drawn
  // dashed with a "~" minute.
  approximate: boolean;
};

// EventMarker renders one event (goal or red card) as an inline
// SVG glyph above the X-axis, with the match minute written directly
// underneath. Rendered via Recharts' <ReferenceDot shape={...}/> so
// the (minute, 0) coordinate is translated into chart pixels for us.
//
// Props shape matches what Recharts injects: {cx, cy, payload: our
// extraProps}. We anchor to a pixel offset above the X-axis so
// markers aren't eaten by the probability lines or axis labels.
type MarkerShapeProps = {
  cx?: number;
  cy?: number;
  payload?: Marker & { pxOffset?: number };
};
export function EventMarker({ cx, cy, payload }: MarkerShapeProps) {
  if (cx == null || !payload) return null;
  // Apply horizontal pixel offset so stacked markers at the same
  // minute don't visually overlap.
  const x = cx + (payload.pxOffset ?? 0);
  const anchorY = (cy ?? 0) + 18;
  const isGoal = payload.kind === 'GOAL';
  const color = sideColor(payload.teamSide);
  // Approximate markers (derived from scoreline, no per-event source)
  // render with a dashed stroke and a tilde-prefixed minute label so
  // the viewer can distinguish them from precise ones.
  const approx = payload.approximate;
  const strokeDash = approx ? '2 2' : undefined;
  return (
    <g>
      <title>{`${payload.label} — ${payload.kind === 'GOAL' ? 'Goal' : 'Red card'}${
        payload.teamSide ? ` (${payload.teamSide})` : ''
      }${payload.description ? ' — ' + payload.description : ''}`}</title>
      {isGoal ? (
        <>
          <circle cx={x} cy={anchorY - 10} r={7} fill="#fff" stroke={color} strokeWidth={2} strokeDasharray={strokeDash} />
          <circle cx={x} cy={anchorY - 10} r={2.4} fill={color} />
        </>
      ) : (
        <rect
          x={x - 5}
          y={anchorY - 17}
          width={10}
          height={14}
          rx={1.5}
          fill={colors.danger}
          stroke="#1a0a0c"
          strokeWidth={0.75}
          strokeDasharray={strokeDash}
        />
      )}
      <text
        x={x}
        y={anchorY + 10}
        textAnchor="middle"
        fontSize={10}
        fontFamily="system-ui, sans-serif"
        fill={approx ? colors.textMuted : colors.textSecondary}
        fontStyle={approx ? 'italic' : 'normal'}
        style={{ fontFeatureSettings: '"tnum"' }}
      >
        {payload.label}
      </text>
    </g>
  );
}
