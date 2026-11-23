import {
  ArrowRightIcon,
  BellIcon,
  CalendarClockIcon,
  CheckIcon,
  ChevronDownIcon,
  ChevronLeftIcon,
  ChevronRightIcon,
  ChevronUpIcon,
  ClockIcon,
  EllipsisIcon,
  FileCodeIcon,
  ImageIcon,
  InboxIcon,
  LinkIcon,
  ListChecksIcon,
  MonitorIcon,
  MoonIcon,
  NetworkIcon,
  PinIcon,
  PlugIcon,
  PlusIcon,
  RefreshCwIcon,
  RepeatIcon,
  SearchIcon,
  SlidersHorizontalIcon,
  SunIcon,
  TriangleAlertIcon,
  XIcon,
  type LucideIcon,
} from "lucide-react";

type IconProps = { size?: number; label?: string };

/**
 * Lucide, sharpened: square caps, miter joins and a constant 1.33px stroke at every size,
 * so the line weight matches the sharp grid. `filled` fills closed shapes in the stroke color.
 * Unlabelled icons are decorative.
 */
function sharp(Glyph: LucideIcon, defaultSize = 16, filled = false) {
  return function Icon({ size = defaultSize, label }: IconProps) {
    return (
      <Glyph
        size={size}
        fill={filled ? "currentColor" : "none"}
        strokeWidth={1.33}
        absoluteStrokeWidth
        strokeLinecap="square"
        strokeLinejoin="miter"
        role={label === undefined ? undefined : "img"}
        aria-hidden={label === undefined ? true : undefined}
        aria-label={label}
        className="shrink-0"
      />
    );
  };
}

/** Dashboard: an inbox tray, the work waiting on you. */
export const IconDashboard = sharp(InboxIcon);
export const IconTasks = sharp(ListChecksIcon);
/** Schedules view: a calendar with a clock. */
export const IconSchedules = sharp(CalendarClockIcon);
export const IconSnippets = sharp(FileCodeIcon);
export const IconSettings = sharp(SlidersHorizontalIcon);
export const IconMore = sharp(EllipsisIcon);
export const IconSearch = sharp(SearchIcon);
export const IconPlus = sharp(PlusIcon);
export const IconCheck = sharp(CheckIcon);
export const IconClose = sharp(XIcon);
export const IconBack = sharp(ChevronLeftIcon);
export const IconForward = sharp(ChevronRightIcon);
/** Level step in the task detail tree bar. */
export const IconStep = sharp(ChevronRightIcon, 12);
export const IconArrowRight = sharp(ArrowRightIcon);
export const IconChevronDown = sharp(ChevronDownIcon);
export const IconChevronUp = sharp(ChevronUpIcon);
/** Theme: monitor (system), sun (light), moon (dark). */
export const IconThemeSystem = sharp(MonitorIcon);
export const IconSun = sharp(SunIcon);
export const IconMoon = sharp(MoonIcon);
export const IconChainLink = sharp(LinkIcon, 12);
export const IconImage = sharp(ImageIcon);
export const IconBell = sharp(BellIcon);
/** Status button: a problem (need or bad tone), still checking, nothing connected. */
export const IconTriangleAlert = sharp(TriangleAlertIcon);
export const IconRefresh = sharp(RefreshCwIcon);
export const IconPlug = sharp(PlugIcon);
/** One-shot start time ("at 02:00"). */
export const IconClock = sharp(ClockIcon, 12);
/** Recurring schedule. */
export const IconRepeat = sharp(RepeatIcon, 12);
/** Task tree: a need over its dependents. */
export const IconTree = sharp(NetworkIcon);
/** Pin a project: always filled, so a 14px pin reads as one solid shape, not a knot of tiny curves. */
export const IconPin = sharp(PinIcon, 12, true);
