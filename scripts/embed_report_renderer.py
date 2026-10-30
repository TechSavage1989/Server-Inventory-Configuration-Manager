"""Embed the editable HTML renderer into the self-contained original collector.
Only the approved HTML function changes; collection and Word/text generation stay
byte-identical to their archived baseline after normalizing scriptVersion.
"""
import argparse, hashlib, json, pathlib

root = pathlib.Path(__file__).resolve().parent.parent
start_marker = b'function Write-ConsolidatedHtmlReport {'
end_marker = b'function Write-DocxTextRun {'

def renderer():
    text = (root/'reporting/Write-ConsolidatedHtmlReport.ps1').read_text(encoding='utf-8')
    text = text.replace('__REPORT_CSS__', (root/'reporting/report.css').read_text(encoding='utf-8').rstrip())
    text = text.replace('__REPORT_JS__', (root/'reporting/report.js').read_text(encoding='utf-8').rstrip())
    assert '__REPORT_' not in text
    return (text.rstrip()+'\n\n').encode('utf-8')

def main():
    parser=argparse.ArgumentParser();parser.add_argument('--check',action='store_true');args=parser.parse_args()
    collector=root/'src/app/Export-ServerInventory.ps1';data=collector.read_bytes()
    start=data.index(start_marker);end=data.index(end_marker,start)
    active=renderer()
    files=['reporting/Write-ConsolidatedHtmlReport.ps1','reporting/report.css','reporting/report.js']
    archived=root/'baseline/v3.0.10/reports/Write-ConsolidatedHtmlReport.ps1'
    metadata={'schema_version':1,'approved_change':'HTML presentation only; original collectors, Word/text reports and block data preserved',
      'original_renderer':{'path':str(archived.relative_to(root)).replace('\\','/'),'sha256':hashlib.sha256(archived.read_bytes()).hexdigest()},
      'sources':{p:hashlib.sha256((root/p).read_bytes()).hexdigest() for p in files},'embedded_renderer_sha256':hashlib.sha256(active).hexdigest()}
    manifest=(json.dumps(metadata,indent=2)+'\n').encode()
    if args.check:
        assert data[start:end]==active,'Embedded HTML renderer differs from reporting source; run scripts/embed_report_renderer.py'
        assert (root/'docs/verification/report-verification.json').read_bytes()==manifest,'Report verification metadata differs from source'
    else:
        collector.write_bytes(data[:start]+active+data[end:]);(root/'docs/verification/report-verification.json').write_bytes(manifest)
    print(('Verified' if args.check else 'Embedded')+' self-contained HTML renderer and scoped provenance metadata.')

if __name__=='__main__':main()
