// The README cover's capture page. `?theme=light|dark` picks the site tokens.
// `?record=<fps>` installs the kit's virtual clock before the scene loads and
// exposes `window.__cover`, so scripts/record-readme-cover.ts steps time one
// frame at a time; without it the scene plays in real time for tuning.

// Only the clock loads before install; the kit and scene load after it.
import { installVirtualClock, type ClockControl } from '../src/vendor/stageshow/kit/clock'

declare global {
  interface Window {
    /** Advances one frame and returns the stage's `data-mark`. */
    __cover?: { step(): Promise<string | null> }
  }
}

function expose(clock: ClockControl) {
  window.__cover = {
    async step() {
      await clock.step(1)
      while (clock.layoutPending) await clock.flushLayout()
      return document.querySelector('.ss-stage')?.getAttribute('data-mark') ?? null
    },
  }
}

async function mount() {
  const params = new URLSearchParams(location.search)
  const theme = params.get('theme') === 'dark' ? 'dark' : 'light'
  document.documentElement.setAttribute('data-vocs-theme', theme)
  const record = params.get('record')
  if (record !== null) expose(installVirtualClock({ fps: Number(record) || 60 }))
  const [{ createRoot }, { ReadmeCover }] = await Promise.all([
    import('react-dom/client'),
    import('../src/components/demos/readme-cover/ReadmeCover'),
    import('../src/pages/_root.css'),
    import('../src/components/stage-look.css'),
    import('./cover.css'),
  ])
  const root = document.getElementById('root')
  if (root === null) throw new Error('cover: #root is missing')
  createRoot(root).render(
    <div className="look-stage">
      <div className="cover-frame">
        <ReadmeCover theme={theme} />
      </div>
    </div>,
  )
}

void mount()
