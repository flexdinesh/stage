// stage's authored 16-unit icons (web/src/components/icons.tsx): 1.5 stroke,
// square caps, miter joins, currentColor.
import type { ReactNode } from 'react'

function Svg({ children, size }: { children: ReactNode; size: number }) {
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
      className="ro-icon"
    >
      {children}
    </svg>
  )
}

export const IconChainLink = ({ size = 12 }: { size?: number }) => (
  <Svg size={size}>
    <path d="M6.5 9.5l3-3M5 7.5L3.5 9a2.1 2.1 0 0 0 3 3L8 10.5M11 8.5L12.5 7a2.1 2.1 0 0 0-3-3L8 5.5" />
  </Svg>
)

export const IconChevronDown = ({ size = 16 }: { size?: number }) => (
  <Svg size={size}>
    <path d="M4 6l4 4 4-4" />
  </Svg>
)

export const IconCheck = ({ size = 12 }: { size?: number }) => (
  <Svg size={size}>
    <path d="M3 8.5l3.5 3.5L13 4.5" />
  </Svg>
)
