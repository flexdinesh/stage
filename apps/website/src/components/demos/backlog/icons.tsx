// stage's authored 16px line icons (web/src/components/icons.tsx): 1.5 stroke,
// square caps, miter joins, currentColor. Paths copied verbatim.
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
      className="bl-icon"
    >
      {children}
    </svg>
  )
}

export const IconPlus = ({ size }: { size?: number }) => (
  <Svg size={size}>
    <path d="M8 3v10M3 8h10" />
  </Svg>
)

export const IconClose = ({ size }: { size?: number }) => (
  <Svg size={size}>
    <path d="M3.5 3.5l9 9M12.5 3.5l-9 9" />
  </Svg>
)

/** The select chevron (index.css `--chevron`). */
export const IconChevron = ({ size }: { size?: number }) => (
  <Svg size={size}>
    <path d="M4 6l4 4 4-4" />
  </Svg>
)
