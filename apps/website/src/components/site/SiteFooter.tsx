import { ThemeToggle } from './ThemeToggle'
import { StageMark } from './StageMark'
import './site-footer.css'

const links = [
  { text: 'Docs', href: '/docs' },
  { text: 'API', href: '/docs/api' },
  { text: 'GitHub', href: 'https://github.com/flexdinesh/stage' },
]

export function SiteFooter() {
  return (
    <div className="site-footer">
      <a className="site-footer__mark" href="/">
        <StageMark />
        stage
      </a>
      <nav className="site-footer__links" aria-label="Footer">
        {links.map((link) => (
          <a key={link.href} href={link.href} {...(link.href.startsWith('http') ? { target: '_blank', rel: 'noreferrer' } : {})}>
            {link.text}
          </a>
        ))}
      </nav>
      <p className="site-footer__meta">MIT licensed · self-hosted</p>
      <ThemeToggle />
    </div>
  )
}
