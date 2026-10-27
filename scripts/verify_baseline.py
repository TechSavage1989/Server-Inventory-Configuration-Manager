"""Verify original collector behavior and archived icons, plus approved active icon hashes."""
import pathlib,json,hashlib,re
root=pathlib.Path(__file__).resolve().parent.parent
expected=json.loads((root/'docs/verification/baseline-verification.json').read_text())['source_files']
for name in ['Run-Inventory.ps1','favicon.ico','app-icon.png','server_inventory.ico','Export-ServerInventory.ps1']:
    # The user explicitly requested the new SVG icon. Preserve original icon
    # hashes and bytes as history rather than relabeling the new art as baseline.
    path=root/'baseline/v3.0.10/icons'/name if name in ['favicon.ico','app-icon.png','server_inventory.ico'] else root/'src/app'/name
    data=path.read_bytes()
    if name=='Export-ServerInventory.ps1':
        data=re.sub(rb"\$scriptVersion = '\d+\.\d+\.\d+'",b"$scriptVersion = '3.0.0'",data,count=1)
        report=json.loads((root/'docs/verification/report-verification.json').read_text())
        start=data.index(b'function Write-ConsolidatedHtmlReport {')
        end=data.index(b'function Write-DocxTextRun {',start)
        current=data[start:end]
        if hashlib.sha256(current).hexdigest()!=report['embedded_renderer_sha256']:raise SystemExit('Approved HTML renderer changed; rebuild and review report metadata')
        original=(root/report['original_renderer']['path']).read_bytes()
        if hashlib.sha256(original).hexdigest()!=report['original_renderer']['sha256']:raise SystemExit('Archived original HTML renderer changed')
        data=data[:start]+original+data[end:]
    if hashlib.sha256(data).hexdigest()!=expected[name]:raise SystemExit('Original asset changed unexpectedly: '+name)
active=json.loads((root/'docs/verification/icon-verification.json').read_text())
for name,metadata in active['assets'].items():
    if hashlib.sha256((root/'src/app'/name).read_bytes()).hexdigest()!=metadata['sha256']:raise SystemExit('Approved active icon changed unexpectedly: '+name)
if hashlib.sha256((root/'src/app'/active['source']).read_bytes()).hexdigest()!=active['source_sha256']:raise SystemExit('Editable SVG source changed; rebuild and review active icon assets')
if hashlib.sha256((root/active['editable_source']).read_bytes()).hexdigest()!=active['editable_source_sha256']:raise SystemExit('Illustrator source changed; rebuild and review icon assets')
print('Original collectors, Word/text generation, wrapper and archived icons match baseline; approved HTML presentation and active icon hashes verified separately.')
