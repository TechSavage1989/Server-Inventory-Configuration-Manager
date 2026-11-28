"""Rebuild docs/demo/index.html, the browser demo served by GitHub Pages.

The demo is the real interface (src/app/index.html) with a simulated backend
(docs/demo/src/*.js and docs/demo/demo-data.js, fictional data only) loaded
before the page's own script. Run it after interface changes:

    python scripts/build_demo.py          # rewrite docs/demo/index.html
    python scripts/build_demo.py --check  # fail when it is out of date
"""
from pathlib import Path
import re, sys

ROOT = Path(__file__).resolve().parents[1]
APP = ROOT / 'src' / 'app'
DEMO = ROOT / 'docs' / 'demo'


def build():
    h = (APP / 'index.html').read_text(encoding='utf-8')
    version = re.search(r'appVersion\s*=\s*"([^"]+)"', (APP / 'main.go').read_text(encoding='utf-8')).group(1)
    h = h.replace('__TOKEN__', 'demo').replace('__APP_VERSION__', version).replace('__COLLECTOR_VERSION__', version)
    h = re.sub(r'href="/favicon\.ico\?v=\d+"', 'href="favicon.ico"', h)
    h = re.sub(r'(href|src)="/app-icon\.png\?v=\d+"', r'\1="app-icon.png"', h)
    h = re.sub(r'src="/app-icon\.svg\?v=\d+"', 'src="app-icon.svg"', h)
    old = re.search(r"function reportFrameUrl\(serial\)\{[^\n]*\}", h)
    if not old:
        raise SystemExit('reportFrameUrl not found in src/app/index.html')
    h = h.replace(old.group(0), "function reportFrameUrl(serial){ return 'report.html'; }")
    h = re.sub(r'<meta http-equiv="Content-Security-Policy"[^>]*>', '', h)
    scripts = ['demo-data.js'] + ['src/' + p.name for p in sorted((DEMO / 'src').glob('*.js'))]
    tags = ''.join('<script src="%s"></script>\n' % s for s in scripts)
    i = h.index('<script>\n')
    h = h[:i] + tags + h[i:]
    h = h.replace('<title>', '<title>Demo · ', 1)
    return h


def main():
    page = build()
    target = DEMO / 'index.html'
    if '--check' in sys.argv:
        if target.read_text(encoding='utf-8') != page:
            raise SystemExit('docs/demo/index.html is out of date: run python scripts/build_demo.py')
        print('Demo page matches src/app/index.html.')
        return
    target.write_text(page, encoding='utf-8')
    print('Wrote', target.relative_to(ROOT))


if __name__ == '__main__':
    main()
