"""Derive the runtime icon without loading Illustrator's external DTD or payload.

This deliberately narrow converter accepts the approved flat-vector vocabulary,
not arbitrary SVG. Unknown drawing features fail closed rather than disappear.
"""
import copy
import hashlib
import json
from pathlib import Path
import re
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[1]
SOURCE = 'src/app/assets/icons/source/server-inventory-illustrator.svg'
SVG = 'http://www.w3.org/2000/svg'
AI = 'http://ns.adobe.com/AdobeIllustrator/10.0/'
INKSCAPE = 'http://www.inkscape.org/namespaces/inkscape'
ET.register_namespace('', SVG)
PRESENTATION = {'fill', 'stroke', 'stroke-width', 'stroke-linecap', 'stroke-linejoin'}
ATTRIBUTES = {'id', 'd', 'cx', 'cy', 'r'} | PRESENTATION


def prepare(data):
    text = data.decode('utf-8')
    # Substitute only namespace URI literals; never parse or resolve a DTD.
    match = re.search(r'<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd" \[(.*?)\]>', text, re.S)
    if not match:
        raise ValueError('Expected Illustrator SVG 1.1 declaration')
    declarations = match.group(1)
    pairs = re.findall(r'<!ENTITY (ns_[a-z_]+) "(http://ns\.adobe\.com/[A-Za-z0-9./]+)">', declarations)
    remainder = re.sub(r'<!ENTITY ns_[a-z_]+ "http://ns\.adobe\.com/[A-Za-z0-9./]+">', '', declarations)
    if remainder.strip() or len(dict(pairs)) != len(pairs):
        raise ValueError('Only literal Adobe namespace declarations are accepted')
    text = text[:match.start()] + text[match.end():]
    for name, uri in pairs:
        text = text.replace('&' + name + ';', uri)
    if re.search(r'<!DOCTYPE|<!ENTITY|<\?(?!xml\s)|&(?!amp;|lt;|gt;|quot;|apos;)', text, re.I):
        raise ValueError('DTD, entity, or processing instruction is not allowed')
    original = ET.fromstring(text)
    if original.tag != '{%s}svg' % SVG or original.get('viewBox') != '0 0 512 512':
        raise ValueError('Expected the approved square SVG canvas')
    permitted_root = {'version', 'id', 'x', 'y', 'viewBox', 'style', '{http://www.w3.org/XML/1998/namespace}space'}
    if (set(original.attrib) - permitted_root or original.get('style') != 'enable-background:new 0 0 512 512;'
            or original.get('x') != '0px' or original.get('y') != '0px' or original.get('version') != '1.1'):
        raise ValueError('Unexpected SVG root attributes')
    classes = {}
    style = original.find('{%s}style' % SVG)
    if style is None or list(style) or style.attrib != {'type': 'text/css'}:
        raise ValueError('Expected a simple Illustrator style sheet')
    css = style.text or ''
    for name, declarations in re.findall(r'\.(st\d+)\{([^{}]+)\}', css):
        attrs = {}
        for declaration in declarations.split(';'):
            if not declaration.strip():
                continue
            key, value = declaration.split(':', 1)
            if key not in PRESENTATION or not re.fullmatch(r'#[0-9A-Fa-f]{6}|none|round|\d+(?:\.\d+)?', value):
                raise ValueError('Unsupported icon style')
            attrs[key] = value
        if name in classes:
            raise ValueError('Duplicate icon style')
        classes[name] = attrs
    if re.sub(r'\.st\d+\{[^{}]+\}', '', css).strip():
        raise ValueError('Unsupported CSS; imports and external references are forbidden')
    switch = original.find('{%s}switch' % SVG)
    if switch is None or switch.attrib:
        raise ValueError('Expected Illustrator switch')
    foreign = switch.find('{%s}foreignObject' % SVG)
    if foreign is None or foreign.attrib != {'requiredExtensions': AI, 'x': '0', 'y': '0', 'width': '1', 'height': '1'}:
        raise ValueError('Unexpected editor-only foreignObject')
    ref = list(foreign)
    if len(ref) != 1 or ref[0].tag != '{%s}aipgfRef' % AI or ref[0].attrib != {'{http://www.w3.org/1999/xlink}href': '#adobe_illustrator_pgf'} or list(ref[0]):
        raise ValueError('Unexpected editor payload reference')
    groups = [e for e in switch if e is not foreign]
    if len(groups) != 1:
        raise ValueError('Expected one vector fallback')
    payload = original.find('{%s}aipgf' % AI)
    if payload is None or list(payload) or set(original) != {style, switch, payload}:
        raise ValueError('Unexpected document nodes')

    def clean(element, inline):
        if element.tag not in {'{%s}%s' % (SVG, tag) for tag in ('g', 'path', 'circle')}:
            raise ValueError('Only editable groups, paths and circles are allowed')
        result = ET.Element(element.tag)
        if element.text and element.text.strip():
            raise ValueError('Unexpected vector text')
        for name, value in element.attrib.items():
            if name.startswith('{%s}' % INKSCAPE) and name.split('}')[1] in {'label', 'groupmode'}:
                continue
            if name == '{%s}extraneous' % AI and value == 'self':
                continue
            if name == 'class' and value in classes:
                result.attrib.update(classes[value] if inline else {'class': value})
                continue
            if name not in ATTRIBUTES or re.search(r'url\s*\(|[<>&]', value, re.I):
                raise ValueError('Unsupported vector attribute: ' + name)
            result.set(name, value)
        for child in element:
            result.append(clean(child, inline))
        return result

    def document(inline):
        root = ET.Element('{%s}svg' % SVG, {'viewBox': '0 0 512 512', 'width': '512', 'height': '512'})
        if not inline:
            root.append(copy.deepcopy(style))
        root.append(clean(groups[0], inline))
        ET.indent(root, space='  ')
        return ET.tostring(root, encoding='unicode') + '\n'

    return {'svg': document(True), 'reference': document(False), 'editable_source': SOURCE,
            'editable_source_sha256': hashlib.sha256(data).hexdigest()}


if __name__ == '__main__':
    print(json.dumps(prepare((ROOT / SOURCE).read_bytes())))
