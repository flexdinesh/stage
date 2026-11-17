// stage's authored 16px icons (web/src/components/icons.tsx): 1.5 stroke,
// square caps, miter joins, currentColor.

export function IconCheck({ size = 16 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.5}
      strokeLinecap="square"
      strokeLinejoin="miter"
      aria-hidden
    >
      <path d="M3 8.5l3.5 3.5L13 4.5" />
    </svg>
  )
}
