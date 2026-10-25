import argparse, struct, pathlib
parser=argparse.ArgumentParser()
parser.add_argument('--input', required=True)
parser.add_argument('--output', required=True)
parser.add_argument('--icon', required=True)
args=parser.parse_args()
base=pathlib.Path(args.input)
out=pathlib.Path(args.output)
ico_path=pathlib.Path(args.icon)
def align(x,a): return (x+a-1)&~(a-1)
ib=ico_path.read_bytes(); _,_,n=struct.unpack_from('<HHH',ib,0)
icons=[]
for i in range(n):
    o=6+i*16; w,h,cc,res,planes,bpp,size,img_off=struct.unpack_from('<BBBBHHII',ib,o)
    icons.append((w,h,cc,planes,bpp,size,ib[img_off:img_off+size],i+1))
buf=bytearray()
def alloc(n,a=4):
    p=align(len(buf),a); buf.extend(b'\0'*(p-len(buf)+n)); return p
root=alloc(32); itype=alloc(16+n*8); gtype=alloc(24)
ilangs=[alloc(24) for _ in icons]; glang=alloc(24)
ides=[alloc(16) for _ in icons]; gde=alloc(16)
grp=bytearray(struct.pack('<HHH',0,1,n))
for w,h,cc,pl,bpp,size,data,rid in icons: grp+=struct.pack('<BBBBHHIH',w,h,cc,0,pl,bpp,size,rid)
iraw=[]
for *_,size,data,rid in icons:
    p=alloc(len(data)); buf[p:p+len(data)]=data; iraw.append(p)
graw=alloc(len(grp)); buf[graw:graw+len(grp)]=grp
def dh(p,c): struct.pack_into('<IIHHHH',buf,p,0,0,0,0,0,c)
def de(p,i,idv,t,d): struct.pack_into('<II',buf,p+16+i*8,idv,(0x80000000 if d else 0)|t)
dh(root,2); de(root,0,3,itype,1); de(root,1,14,gtype,1)
dh(itype,n)
for i,ic in enumerate(icons): de(itype,i,ic[-1],ilangs[i],1)
dh(gtype,1); de(gtype,0,1,glang,1)
for i,p in enumerate(ilangs): dh(p,1); de(p,0,1033,ides[i],0)
dh(glang,1); de(glang,0,1033,gde,0)
for i,ic in enumerate(icons): struct.pack_into('<IIII',buf,ides[i],0,ic[5],0,0)
struct.pack_into('<IIII',buf,gde,0,len(grp),0,0)
pe=bytearray(base.read_bytes()); peoff=struct.unpack_from('<I',pe,0x3c)[0]; nsec=struct.unpack_from('<H',pe,peoff+6)[0]; optsz=struct.unpack_from('<H',pe,peoff+20)[0]; opt=peoff+24
magic=struct.unpack_from('<H',pe,opt)[0]; sec_align=struct.unpack_from('<I',pe,opt+32)[0]; file_align=struct.unpack_from('<I',pe,opt+36)[0]; headers=struct.unpack_from('<I',pe,opt+60)[0]
sectab=opt+optsz; last=sectab+(nsec-1)*40
last_vs,last_va,last_rawsz,last_rawptr=struct.unpack_from('<IIII',pe,last+8)
new_va=align(last_va+max(last_vs,last_rawsz),sec_align)
new_raw=align(len(pe),file_align); rawsz=align(len(buf),file_align)
newhdr=sectab+nsec*40
if newhdr+40>headers: raise SystemExit('no section header room')
for i,p in enumerate(ides): struct.pack_into('<I',buf,p,new_va+iraw[i])
struct.pack_into('<I',buf,gde,new_va+graw)
if len(pe)<new_raw: pe.extend(b'\0'*(new_raw-len(pe)))
pe.extend(buf); pe.extend(b'\0'*(rawsz-len(buf)))
pe[newhdr:newhdr+8]=b'.rsrc\0\0\0'
struct.pack_into('<IIIIIIHHI',pe,newhdr+8,len(buf),new_va,rawsz,new_raw,0,0,0,0,0x40000040)
struct.pack_into('<H',pe,peoff+6,nsec+1)
struct.pack_into('<I',pe,opt+56,align(new_va+len(buf),sec_align))
if magic==0x20b: dd=opt+112
else: dd=opt+96
struct.pack_into('<II',pe,dd+16,new_va,len(buf))
out.write_bytes(pe)
print(out)