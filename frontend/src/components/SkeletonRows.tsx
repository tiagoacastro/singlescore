// Placeholder rows shown while a list loads.
export function SkeletonRows({ count = 3 }: { count?: number }) {
  return (
    <>
      {Array.from({ length: count }, (_, i) => (
        <div key={i} className="skeleton skeleton--row" />
      ))}
    </>
  );
}
