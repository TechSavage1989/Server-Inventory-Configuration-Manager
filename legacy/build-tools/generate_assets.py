import argparse, binascii, pathlib, struct, zlib

def chunk(kind, data):
    return struct.pack('>I', len(data)) + kind + data + struct.pack('>I', binascii.crc32(kind + data) & 0xffffffff)

def png_bytes(size):
    bg=(45,108,223,255); white=(255,255,255,255); clear=(0,0,0,0)
    px=[[clear for _ in range(size)] for _ in range(size)]
    def inside_round(x,y,x0,y0,x1,y1,r):
        if x0+r <= x <= x1-r or y0+r <= y <= y1-r: return x0 <= x <= x1 and y0 <= y <= y1
        cx=x0+r if x < x0+r else x1-r
        cy=y0+r if y < y0+r else y1-r
        return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r
    r=max(1,round(size*0.19))
    for y in range(size):
        for x in range(size):
            if inside_round(x,y,0,0,size-1,size-1,r): px[y][x]=bg
    def rect(x0,y0,x1,y1,color):
        for y in range(max(0,y0),min(size,y1)):
            for x in range(max(0,x0),min(size,x1)): px[y][x]=color
    def rrect(x0,y0,x1,y1,rad,color):
        for y in range(max(0,y0),min(size,y1)):
            for x in range(max(0,x0),min(size,x1)):
                if inside_round(x,y,x0,y0,x1-1,y1-1,rad): px[y][x]=color
    scale=size/256.0
    def S(v): return int(round(v*scale))
    rect(S(120),S(91),S(136),S(165),white)
    for y0 in (48,148):
        rrect(S(60),S(y0),S(196),S(y0+60),max(1,S(11)),white)
    rect(S(76),S(64),S(180),S(92),bg)
    rect(S(76),S(164),S(180),S(192),bg)
    raw=b''.join(b'\x00'+b''.join(bytes(p) for p in row) for row in px)
    return b'\x89PNG\r\n\x1a\n'+chunk(b'IHDR',struct.pack('>IIBBBBB',size,size,8,6,0,0,0))+chunk(b'IDAT',zlib.compress(raw,9))+chunk(b'IEND',b'')

def ico_bytes(sizes):
    images=[(s,png_bytes(s)) for s in sizes]
    header=struct.pack('<HHH',0,1,len(images))
    offset=6+16*len(images); entries=[]; payload=[]
    for size,data in images:
        dim=0 if size>=256 else size
        entries.append(struct.pack('<BBBBHHII',dim,dim,0,0,1,32,len(data),offset))
        payload.append(data); offset += len(data)
    return header+b''.join(entries)+b''.join(payload)

def main():
    ap=argparse.ArgumentParser(); ap.add_argument('--output-root',default='.')
    args=ap.parse_args(); root=pathlib.Path(args.output_root); root.mkdir(parents=True,exist_ok=True)
    (root/'app-icon.png').write_bytes(png_bytes(256))
    ico=ico_bytes([16,20,24,32,40,48,64,128,256])
    (root/'favicon.ico').write_bytes(ico)
    (root/'server_inventory.ico').write_bytes(ico)
    print(root/'app-icon.png'); print(root/'favicon.ico'); print(root/'server_inventory.ico')
if __name__=='__main__': main()