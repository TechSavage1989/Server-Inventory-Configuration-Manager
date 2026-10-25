/* Render the editable SVG into standard PNG-backed Windows ICO frames.
   Build-time only. No renderer or Node dependency ships with the application. */
const fs=require('fs'),path=require('path'),crypto=require('crypto'),assert=require('assert');
const {Resvg}=require('./icon-tools/node_modules/@resvg/resvg-js');
const root=path.resolve(__dirname,'..'),source='assets/icons/server-inventory.svg';
const svg=fs.readFileSync(path.join(root,source));
const text=svg.toString('utf8');
const palette=[...new Set([...text.matchAll(/(?:fill|stroke)="(#[0-9A-Fa-f]{6})"/g)].map(m=>m[1].toUpperCase()))];
assert(!/<\s*(image|script|filter|linearGradient|radialGradient|foreignObject)\b/i.test(text),'Icon must contain only editable flat vectors');
assert(!/\b(?:href|style|opacity)\s*=/i.test(text),'Icon must not embed external/raster content or shading');
assert.equal(palette.length,4,'Keep the icon to four solid colors');
const sizes=[16,20,24,32,40,48,64,96,128,256];
const frames=sizes.map(size=>new Resvg(svg,{fitTo:{mode:'width',value:size},font:{loadSystemFonts:false}}).render().asPng());
const header=Buffer.alloc(6+sizes.length*16);header.writeUInt16LE(1,2);header.writeUInt16LE(sizes.length,4);
let offset=header.length;
sizes.forEach((size,i)=>{const at=6+i*16;header[at]=size===256?0:size;header[at+1]=size===256?0:size;header.writeUInt16LE(1,at+4);header.writeUInt16LE(32,at+6);header.writeUInt32LE(frames[i].length,at+8);header.writeUInt32LE(offset,at+12);offset+=frames[i].length;});
const ico=Buffer.concat([header,...frames]);
const outputs={'favicon.ico':ico,'server_inventory.ico':ico,'app-icon.png':frames[frames.length-1]};
const hash=data=>crypto.createHash('sha256').update(data).digest('hex');
const manifest={schema_version:1,source,source_sha256:hash(svg),palette,ico_sizes:sizes,renderer:'@resvg/resvg-js@2.6.2',assets:Object.fromEntries(Object.entries(outputs).map(([name,data])=>[name,{sha256:hash(data),size:data.length}]))};
outputs['icon-verification.json']=Buffer.from(JSON.stringify(manifest,null,2)+'\n');
const check=process.argv.includes('--check');
for(const [name,data] of Object.entries(outputs)){
 if(check){assert(fs.existsSync(path.join(root,name)),name+' is missing');assert(fs.readFileSync(path.join(root,name)).equals(data),name+' differs from the editable SVG; run node scripts/build_icons.cjs');}
 else fs.writeFileSync(path.join(root,name),data);
}
console.log((check?'Verified':'Generated')+' editable flat SVG, transparent 256px PNG, and '+sizes.length+' Windows ICO sizes.');
