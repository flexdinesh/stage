// stage's authored 16px line icons (packages/app icons): 1.5 stroke, square
// caps, miter joins, currentColor.
import type { ReactNode } from 'react'

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
      aria-hidden="true"
    >
      {children}
    </svg>
  )
}

export const IconCheck = ({ size }: { size?: number }) => (
  <Svg size={size}>
    <path d="M3 8.5l3.5 3.5L13 4.5" />
  </Svg>
)

export const IconPlus = ({ size }: { size?: number }) => (
  <Svg size={size}>
    <path d="M8 3v10M3 8h10" />
  </Svg>
)

/** The composer's send arrow. */
export const IconSend = ({ size }: { size?: number }) => (
  <Svg size={size}>
    <path d="M8 13V3.5M4 7.5l4-4 4 4" />
  </Svg>
)
