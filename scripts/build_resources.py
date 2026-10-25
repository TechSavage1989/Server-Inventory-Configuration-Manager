"""Build a Windows COFF .rsrc object from the approved active icon and exact version metadata.
Resources are linked by Go; the compiled executable is never patched afterward.
"""
import argparse,pathlib,struct

def align(n,a=4):return (n+a-1)&~(a-1)
def wide(s):return (s+'\0').encode('utf-16le')
def block(key,value=b'',children=(),text=False):
    data=bytearray(struct.pack('<HHH',0,len(value)//2 if text else len(value),1 if text else 0)+wide(key))
    data.extend(b'\0'*(align(len(data))-len(data)));data.extend(value)
    for child in children:
        data.extend(b'\0'*(align(len(data))-len(data)));data.extend(child)
    struct.pack_into('<H',data,0,len(data));return bytes(data)
def version_info(version):
    major,minor,patch=map(int,version.split('.'))
    if any(v<0 or v>65535 for v in [major,minor,patch]):raise ValueError('version outside Windows metadata range')
    ms=(major<<16)|minor;ls=patch<<16
    fixed=struct.pack('<13I',0xfeef04bd,0x10000,ms,ls,ms,ls,0x3f,0,0x40004,1,0,0,0)
    values={'CompanyName':'TechSavage1989','FileDescription':'Server Inventory Configuration Manager','FileVersion':version,'InternalName':'Server-Inventory-Configuration-Manager','OriginalFilename':'Server-Inventory-Configuration-Manager.exe','ProductName':'Server Inventory Configuration Manager','ProductVersion':version}
    table=block('040904b0',children=[block(k,wide(v),text=True) for k,v in values.items()])
    strings=block('StringFileInfo',children=[table])
    variables=block('VarFileInfo',children=[block('Translation',struct.pack('<HH',1033,1200))])
    return block('VS_VERSION_INFO',fixed,[strings,variables])
def build(icon,version):
    ico=pathlib.Path(icon).read_bytes();reserved,kind,count=struct.unpack_from('<HHH',ico)
    if reserved!=0 or kind!=1 or count==0:raise ValueError('invalid icon')
    icons=[];group=bytearray(struct.pack('<HHH',0,1,count))
    for i in range(count):
        w,h,cc,r,planes,bpp,size,offset=struct.unpack_from('<BBBBHHII',ico,6+i*16)
        payload=ico[offset:offset+size]
        if len(payload)!=size:raise ValueError('truncated icon')
        icons.append((i+1,payload));group.extend(struct.pack('<BBBBHHIH',w,h,cc,0,planes,bpp,size,i+1))
    tree={3:{rid:{1033:payload} for rid,payload in icons},14:{1:{1033:bytes(group)}},16:{1:{1033:version_info(version)}}}
    buf=bytearray();relocations=[]
    def alloc(size):
        offset=align(len(buf));buf.extend(b'\0'*(offset-len(buf)+size));return offset
    def directory(children):
        offset=alloc(16+8*len(children));struct.pack_into('<IIHHHH',buf,offset,0,0,0,0,0,len(children))
        for i,(key,value) in enumerate(sorted(children.items())):
            if isinstance(value,dict):target=directory(value)|0x80000000
            else:
                target=alloc(16);raw=alloc(len(value));buf[raw:raw+len(value)]=value
                struct.pack_into('<IIII',buf,target,raw,len(value),0,0);relocations.append(target)
            struct.pack_into('<II',buf,offset+16+i*8,key,target)
        return offset
    assert directory(tree)==0
    raw_start=60;reloc_start=raw_start+len(buf);symbol_start=reloc_start+10*len(relocations)
    header=struct.pack('<HHIIIHH',0x8664,1,0,symbol_start,1,0,0)
    section=struct.pack('<8sIIIIIIHHI',b'.rsrc\0\0\0',0,0,len(buf),raw_start,reloc_start,0,len(relocations),0,0x40000040)
    relocs=b''.join(struct.pack('<IIH',offset,0,3) for offset in relocations)
    symbol=struct.pack('<8sIhHBB',b'.rsrc\0\0\0',0,1,0,3,0)
    return header+section+buf+relocs+symbol+struct.pack('<I',4)
if __name__=='__main__':
    parser=argparse.ArgumentParser();parser.add_argument('--icon',default='server_inventory.ico');parser.add_argument('--version',required=True);parser.add_argument('--output',default='application_windows_amd64.syso');args=parser.parse_args()
    pathlib.Path(args.output).write_bytes(build(args.icon,args.version))
    print(args.output)
