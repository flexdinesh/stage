import type { ReactNode } from 'react'

// stage's authored 16px icons (web/src/components/icons.tsx): 1.5 stroke,
// square caps, miter joins, currentColor.
function Svg({ size = 16, children }: { size?: number; children: ReactNode }) {
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
      {children}
    </svg>
  )
}

export const IconBell = ({ size }: { size?: number }) => (
  <Svg size={size}>
    <path d="M4.5 11.5V6.5a3.5 3.5 0 0 1 7 0v5M2.5 11.5h11M6.5 14h3" />
  </Svg>
)

export const IconSearch = ({ size }: { size?: number }) => (
  <Svg size={size}>
    <path d="M7 2.5a4.5 4.5 0 1 1 0 9 4.5 4.5 0 0 1 0-9zM10.5 10.5l3 3" />
  </Svg>
)

export const IconPlus = ({ size }: { size?: number }) => (
  <Svg size={size}>
    <path d="M8 3v10M3 8h10" />
  </Svg>
)

/** Canonical app artwork, shared by both notification placements. */
export const StageLogo = () => (
  <img src="/logo.svg" width="512" height="512" alt="" style={{ width: '100%', height: '100%' }} />
)
