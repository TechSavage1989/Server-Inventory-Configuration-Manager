"""Reject repository-root clutter in checkouts and extracted source archives."""
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]
FILES = {'.gitattributes', '.gitignore', 'README.md', 'AGENTS.md', 'DEVELOPMENT_HANDOFF.md', 'go.mod'}
DIRECTORIES = {'.github', 'src', 'scripts', 'tests', 'docs', 'baseline', 'legacy', 'release', 'reporting'}
GENERATED = {'.git', 'build', 'dist'}

def violations(paths):
    return sorted(p for p in paths if (p.split('/')[0] not in DIRECTORIES and p not in FILES)
                  or '__pycache__' in Path(p).parts or p.endswith('.pyc'))

def main():
    # Tracked generated files are rejected too; ignored local build output is allowed.
    if (ROOT / '.git').exists():
        paths = subprocess.check_output(['git', 'ls-files', '-z'], cwd=ROOT).decode().split('\0')
        bad = violations([p for p in paths if p])
    else:
        bad = []
    for p in ROOT.iterdir():
        if p.name in GENERATED:
            continue
        if p.is_file() and p.name not in FILES or p.is_dir() and p.name not in DIRECTORIES:
            bad.append(p.name)
    if bad:
        raise SystemExit('Unexpected root entries: ' + ', '.join(sorted(set(bad))))
    required = ['src/app/main.go', 'src/app/index.html', 'src/app/Export-ServerInventory.ps1',
                'src/app/assets/icons/server-inventory.svg', 'docs/VALIDATION.md',
                'docs/verification/baseline-verification.json', 'release/VERSION']
    missing = [p for p in required if not (ROOT / p).is_file()]
    if missing:
        raise SystemExit('Missing layout inputs: ' + ', '.join(missing))
    print('Repository layout verified: 15 intentional root entries; application inputs stay together.')

if __name__ == '__main__':
    main()
