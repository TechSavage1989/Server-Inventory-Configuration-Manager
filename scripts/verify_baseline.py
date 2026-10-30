"""Verify protected original bytes plus separately approved collector/icon changes."""
import pathlib
import json
import re
from collector_provenance import restore_reviewed_regions, sha256

root = pathlib.Path(__file__).resolve().parent.parent
expected = json.loads((root / 'docs/verification/baseline-verification.json').read_text())['source_files']
report = json.loads((root / 'docs/verification/report-verification.json').read_text())
original_renderer = (root / report['original_renderer']['path']).read_bytes()
if sha256(original_renderer) != report['original_renderer']['sha256']:
    raise SystemExit('Archived original HTML renderer changed')
changes = json.loads((root / 'docs/verification/collector-change-verification.json').read_text())
published_archives = {
    'Export-ServerInventory.ps1': 'b072c3cf15f1cef14df397718a311a753cfae0a7b12ebf4380fbed4a2d01c091',
    'Run-Inventory.ps1': 'dfb7922b697bf6110eacd02fff1053ec5b0a3724dd5e4c1d98d6c4dbe45baec7',
}
if changes['schema_version'] != 1 or set(changes['files']) != set(published_archives):
    raise SystemExit('Invalid approved collector change manifest')
for name, digest in published_archives.items():
    if changes['files'][name]['archive_sha256'] != digest:
        raise SystemExit('Published 3.2.10 archive identity must not be relabeled: ' + name)
if 'reviewed_diff' in changes:
    reviewed = changes['reviewed_diff']
    if sha256((root / reviewed['path']).read_bytes()) != reviewed['sha256']:
        raise SystemExit('Reviewed collector diff does not match its recorded hash')



def normalize_collector(data, validate_current_renderer):
    data = re.sub(rb"\$scriptVersion = '\d+\.\d+\.\d+'", b"$scriptVersion = '3.0.0'", data, count=1)
    start = data.index(b'function Write-ConsolidatedHtmlReport {')
    end = data.index(b'function Write-DocxTextRun {', start)
    if validate_current_renderer and sha256(data[start:end]) != report['embedded_renderer_sha256']:
        raise ValueError('Approved HTML renderer changed; rebuild and review report metadata')
    return data[:start] + original_renderer + data[end:]


for name in ['Run-Inventory.ps1', 'favicon.ico', 'app-icon.png', 'server_inventory.ico', 'Export-ServerInventory.ps1']:
    is_icon = name in ['favicon.ico', 'app-icon.png', 'server_inventory.ico']
    path = root / 'baseline/v3.0.10/icons' / name if is_icon else root / 'src/app' / name
    data = path.read_bytes()
    try:
        if name in changes['files']:
            entry = changes['files'][name]
            archive = (root / entry['archive_path']).read_bytes()
            if sha256(archive) != entry['archive_sha256']:
                raise ValueError('Published 3.2.10 collector archive changed: ' + name)
            if name == 'Export-ServerInventory.ps1':
                data = normalize_collector(data, True)
                archive = normalize_collector(archive, False)
            data = restore_reviewed_regions(data, archive, entry['regions'])
        elif name == 'Export-ServerInventory.ps1':
            data = normalize_collector(data, True)
    except (ValueError, KeyError) as error:
        raise SystemExit(str(error))
    if sha256(data) != expected[name]:
        raise SystemExit('Original asset changed unexpectedly: ' + name)
active = json.loads((root / 'docs/verification/icon-verification.json').read_text())
for name, metadata in active['assets'].items():
    if sha256((root / 'src/app' / name).read_bytes()) != metadata['sha256']:
        raise SystemExit('Approved active icon changed unexpectedly: ' + name)
if sha256((root / 'src/app' / active['source']).read_bytes()) != active['source_sha256']:
    raise SystemExit('Editable SVG source changed; rebuild and review active icon assets')
if sha256((root / active['editable_source']).read_bytes()) != active['editable_source_sha256']:
    raise SystemExit('Illustrator source changed; rebuild and review icon assets')
print('Original collector/Word/text/wrapper bytes verified outside separately pinned reviewed changes; original and active icon provenance verified.')
