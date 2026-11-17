import './stage-mark.css'

/** Stage's 16-unit app glyph; decorative beside the visible wordmark. */
export function StageMark({ needsInput = false }: { needsInput?: boolean }) {
  return (
    <svg className="stage-mark" width="16" height="16" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
      <rect x="1" y="11" width="14" height="3" />
      <rect x="3" y="7" width="10" height="3" opacity=".72" />
      <rect className="stage-mark__top" x="5" y="3" width="6" height="3" data-needs-input={needsInput || undefined} />
    </svg>
  )
}
