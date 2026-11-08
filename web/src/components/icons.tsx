import type { ReactNode } from "react";

/** Authored 16px line icons: 1.5 stroke, square caps, miter joins (matches the sharp grid). */
function Svg({ children, size = 16, label }: { children: ReactNode; size?: number; label?: string }) {
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
      role={label === undefined ? undefined : "img"}
      aria-hidden={label === undefined ? true : undefined}
      aria-label={label}
      className="shrink-0"
    >
      {children}
    </svg>
  );
}

type IconProps = { size?: number; label?: string };

export const IconStage = (p: IconProps) => (
  <Svg {...p}>
    <path d="M2.5 2.5h4.5v4.5H2.5zM9 2.5h4.5v4.5H9zM2.5 9h4.5v4.5H2.5zM9 9h4.5v4.5H9z" />
  </Svg>
);

export const IconProjects = (p: IconProps) => (
  <Svg {...p}>
    <path d="M2 3.5h4.5l1.5 1.5h6v7.5H2z" />
  </Svg>
);

export const IconTemplates = (p: IconProps) => (
  <Svg {...p}>
    <path d="M3.5 2h9v12h-9zM6 5.5h4M6 8h4M6 10.5h2.5" />
  </Svg>
);

export const IconHistory = (p: IconProps) => (
  <Svg {...p}>
    <path d="M8 2.5a5.5 5.5 0 1 1-5.2 3.7M2.5 2.5v3.5H6M8 5v3.2l2.2 1.3" />
  </Svg>
);

export const IconMore = (p: IconProps) => (
  <Svg {...p}>
    <path d="M3 7.5h1.5v1.5H3zM7.25 7.5h1.5v1.5h-1.5zM11.5 7.5H13v1.5h-1.5z" />
  </Svg>
);

export const IconSearch = (p: IconProps) => (
  <Svg {...p}>
    <path d="M7 2.5a4.5 4.5 0 1 1 0 9 4.5 4.5 0 0 1 0-9zM10.5 10.5l3 3" />
  </Svg>
);

export const IconPlus = (p: IconProps) => (
  <Svg {...p}>
    <path d="M8 3v10M3 8h10" />
  </Svg>
);

export const IconExternal = (p: IconProps) => (
  <Svg {...p}>
    <path d="M6 3H3v10h10v-3M9 3h4v4M13 3L7.5 8.5" />
  </Svg>
);

export const IconClose = (p: IconProps) => (
  <Svg {...p}>
    <path d="M3.5 3.5l9 9M12.5 3.5l-9 9" />
  </Svg>
);

export const IconBack = (p: IconProps) => (
  <Svg {...p}>
    <path d="M10 3L5 8l5 5" />
  </Svg>
);

export const IconArrowRight = (p: IconProps) => (
  <Svg {...p}>
    <path d="M2.5 8h10M9 4.5L12.5 8 9 11.5" />
  </Svg>
);

export const IconChevronDown = (p: IconProps) => (
  <Svg {...p}>
    <path d="M4 6l4 4 4-4" />
  </Svg>
);

/** Theme: square split half filled (system), sun (light), moon (dark). */
export const IconThemeSystem = (p: IconProps) => (
  <Svg {...p}>
    <path d="M2.5 2.5h11v11h-11z" />
    <path d="M8 2.5v11h5.5v-11z" fill="currentColor" stroke="none" />
  </Svg>
);

export const IconSun = (p: IconProps) => (
  <Svg {...p}>
    <path d="M5.5 5.5h5v5h-5zM8 1.5v1.5M8 13v1.5M1.5 8H3M13 8h1.5M3.4 3.4l1 1M11.6 11.6l1 1M3.4 12.6l1-1M11.6 4.4l1-1" />
  </Svg>
);

export const IconMoon = (p: IconProps) => (
  <Svg {...p}>
    <path d="M13 9.5A5.5 5.5 0 1 1 6.5 3a4.5 4.5 0 0 0 6.5 6.5z" />
  </Svg>
);

export const IconSync = (p: IconProps) => (
  <Svg {...p}>
    <path d="M13 6a5 5 0 0 0-9.2-1.5M3 10a5 5 0 0 0 9.2 1.5M3 2v3h3M13 14v-3h-3" />
  </Svg>
);

export const IconChainLink = (p: IconProps) => (
  <Svg {...p} size={p.size ?? 12}>
    <path d="M6.5 9.5l3-3M5 7.5L3.5 9a2.1 2.1 0 0 0 3 3L8 10.5M11 8.5L12.5 7a2.1 2.1 0 0 0-3-3L8 5.5" />
  </Svg>
);
