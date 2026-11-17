import type { ReactNode } from 'react'

// stage's authored 16px line icons (web/src/components/icons.tsx):
// 1.5 stroke, square caps, miter joins, currentColor.
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
      className="hdb-icon"
    >
      {children}
    </svg>
  )
}

type P = { size?: number }

export const IconSearch = (p: P) => (
  <Svg {...p}>
    <path d="M7 2.5a4.5 4.5 0 1 1 0 9 4.5 4.5 0 0 1 0-9zM10.5 10.5l3 3" />
  </Svg>
)

export const IconPlus = (p: P) => (
  <Svg {...p}>
    <path d="M8 3v10M3 8h10" />
  </Svg>
)

export const IconCheck = (p: P) => (
  <Svg {...p}>
    <path d="M3 8.5l3.5 3.5L13 4.5" />
  </Svg>
)

export const IconChevronDown = (p: P) => (
  <Svg {...p}>
    <path d="M4 6l4 4 4-4" />
  </Svg>
)

export const IconThemeSystem = (p: P) => (
  <Svg {...p}>
    <path d="M2.5 2.5h11v11h-11z" />
    <path d="M8 2.5v11h5.5v-11z" fill="currentColor" stroke="none" />
  </Svg>
)
