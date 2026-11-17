import type { ReactNode } from 'react'

// stage's authored icons (web/src/components/icons.tsx): 16-unit grid, 1.5
// stroke, square caps, miter joins, currentColor.
function Svg({ children, size = 16 }: { children: ReactNode; size?: number }) {
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
      className="sch-icon"
    >
      {children}
    </svg>
  )
}

type P = { size?: number }

export const IconRepeat = ({ size = 12 }: P) => (
  <Svg size={size}>
    <path d="M2.5 7.5V4.5h10M10.5 2.5l2 2-2 2M13.5 8.5v3h-10M5.5 13.5l-2-2 2-2" />
  </Svg>
)

export const IconPlus = ({ size = 16 }: P) => (
  <Svg size={size}>
    <path d="M8 3v10M3 8h10" />
  </Svg>
)

export const IconSearch = ({ size = 16 }: P) => (
  <Svg size={size}>
    <path d="M7 2.5a4.5 4.5 0 1 1 0 9 4.5 4.5 0 0 1 0-9zM10.5 10.5l3 3" />
  </Svg>
)

export const IconCheck = ({ size = 16 }: P) => (
  <Svg size={size}>
    <path d="M3 8.5l3.5 3.5L13 4.5" />
  </Svg>
)

export const IconClose = ({ size = 16 }: P) => (
  <Svg size={size}>
    <path d="M3.5 3.5l9 9M12.5 3.5l-9 9" />
  </Svg>
)

export const IconChevronDown = ({ size = 16 }: P) => (
  <Svg size={size}>
    <path d="M4 6l4 4 4-4" />
  </Svg>
)
