import type { ReactNode } from 'react'
import { Demo as HeroDemo } from '../demos/hero-dashboard'
import { Demo as BacklogDemo } from '../demos/backlog'
import { Demo as RunOrderDemo } from '../demos/run-order'
import { Demo as SchedulesDemo } from '../demos/schedules'
import { Demo as PlanSessionDemo } from '../demos/plan-session'
import { Demo as PwaPushDemo } from '../demos/pwa-push'
import { closing, features, hero, type Feature, type FeatureId } from './copy'
import '../stage-look.css'
import './landing.css'

const demos: Record<FeatureId, () => ReactNode> = {
  backlog: BacklogDemo,
  'run-order': RunOrderDemo,
  schedules: SchedulesDemo,
  'plan-session': PlanSessionDemo,
  'pwa-push': PwaPushDemo,
}

function Arrow() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path d="M3 8h9M8.5 4.5 12 8l-3.5 3.5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="square" />
    </svg>
  )
}

function External() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path d="M5.5 4.5h6v6M11.5 4.5l-7 7" stroke="currentColor" strokeWidth="1.5" strokeLinecap="square" />
    </svg>
  )
}

function Actions({ primary, secondary }: { primary: { text: string; href: string }; secondary: { text: string; href: string } }) {
  const external = secondary.href.startsWith('http')
  return (
    <div className="lp-actions">
      <a className="lp-btn lp-btn--primary" href={primary.href}>
        {primary.text}
        <Arrow />
      </a>
      <a className="lp-btn lp-btn--secondary" href={secondary.href} {...(external ? { target: '_blank', rel: 'noreferrer' } : {})}>
        {secondary.text}
        {external ? <External /> : <Arrow />}
      </a>
    </div>
  )
}

function FeatureSection({ feature }: { feature: Feature }) {
  const Visual = demos[feature.id]
  return (
    <section className="lp-feature" id={feature.id} aria-labelledby={`${feature.id}-title`}>
      <div className="lp-feature__copy">
        <h2 className="lp-h2" id={`${feature.id}-title`}>
          {feature.title}
        </h2>
        <p className="lp-body">{feature.body}</p>
        <ul className="lp-facts">
          {feature.facts.map((fact) => (
            <li key={fact}>{fact}</li>
          ))}
        </ul>
        <a className="lp-link" href={feature.link.href}>
          {feature.link.text}
          <Arrow />
        </a>
      </div>
      <div className="lp-feature__visual lp-demo" role="img" aria-label={feature.demoLabel}>
        <div inert>
          <Visual />
        </div>
      </div>
    </section>
  )
}

export function Landing() {
  return (
    <div className="lp look-stage">
      <section className="lp-hero" aria-labelledby="hero-title">
        <div className="lp-hero__copy">
          <h1 className="lp-h1" id="hero-title">
            {hero.title}
          </h1>
          <p className="lp-lede">{hero.body}</p>
          <p className="lp-meta">
            <b>//</b> {hero.meta}
          </p>
          <Actions primary={hero.primary} secondary={hero.secondary} />
        </div>
        <div className="lp-hero__visual lp-demo" role="img" aria-label={hero.demoLabel}>
          <div inert>
            <HeroDemo />
          </div>
        </div>
      </section>

      {features.map((feature) => (
        <FeatureSection key={feature.id} feature={feature} />
      ))}

      <section className="lp-closing" aria-labelledby="closing-title">
        <div className="lp-closing__copy">
          <h2 className="lp-h2" id="closing-title">
            {closing.title}
          </h2>
          <p className="lp-body">{closing.body}</p>
          <Actions primary={closing.primary} secondary={closing.secondary} />
        </div>
        <figure className="lp-panel">
          <figcaption className="lp-panel__header">Docker compose</figcaption>
          <pre className="lp-panel__body">
            <code>
              {closing.quickstart.map((line) => (
                <span key={line} className="lp-cmd">
                  {line}
                </span>
              ))}
              <span className="lp-cmd lp-cmd--note">{closing.quickstartNote}</span>
            </code>
          </pre>
        </figure>
      </section>
    </div>
  )
}
