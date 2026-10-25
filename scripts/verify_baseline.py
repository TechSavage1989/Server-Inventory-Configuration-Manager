"""Verify that original collection behavior and icon bytes remain the build inputs."""
import pathlib,json,hashlib,re
root=pathlib.Path(__file__).resolve().parent.parent
expected=json.loads((root/'baseline-verification.json').read_text())['source_files']
for name in ['Run-Inventory.ps1','favicon.ico','app-icon.png','server_inventory.ico','Export-ServerInventory.ps1']:
    data=(root/name).read_bytes()
    if name=='Export-ServerInventory.ps1':
        data=re.sub(rb"\$scriptVersion = '\d+\.\d+\.\d+'",b"$scriptVersion = '3.0.0'",data,count=1)
    if hashlib.sha256(data).hexdigest()!=expected[name]:raise SystemExit('Original asset changed unexpectedly: '+name)
print('Original v3.0.10 collector, wrapper and icons match baseline bytes (collector version normalized).')
