"""Positive and hostile-input controls for the build-only Illustrator converter."""
from pathlib import Path
import sys
import unittest
import xml.etree.ElementTree as ET

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'scripts'))
from prepare_icon import prepare, ROOT, SOURCE


class IconSourceTests(unittest.TestCase):
    def setUp(self):
        self.source = (ROOT / SOURCE).read_bytes()

    def test_source_identity_and_runtime_vector_inventory(self):
        result = prepare(self.source)
        self.assertEqual(result['editable_source_sha256'], '4eb6eb78435ebaf64dbc5b71c836af8f1b6544235bba3adbfcd0a7e2a7c3a775')
        self.assertEqual(len(self.source), 49437)
        svg = result['svg']
        for forbidden in ['DOCTYPE', 'ENTITY', 'foreignObject', 'aipgf', '<style', 'class=', 'href=', 'onload=', '<image', '<script']:
            self.assertNotIn(forbidden, svg)
        root = ET.fromstring(svg)
        self.assertEqual(root.attrib['viewBox'], '0 0 512 512')
        self.assertEqual(sum(e.tag.endswith('}path') for e in root.iter()), 8)
        self.assertEqual(sum(e.tag.endswith('}circle') for e in root.iter()), 5)

    def test_unapproved_active_content_fails_closed(self):
        mutations = [
            (b'x="0px" y="0px"', b'x="20px" y="0px"'),
            (b'<switch>', b'<script>alert(1)</script><switch>'),
            (b'id="chassis"', b'id="chassis" onload="alert(1)"'),
            (b'id="chassis"', b'id="chassis" href="https://example.invalid/icon"'),
            (b'.st0{fill:#132F4C;}', b'@import url(https://example.invalid/css);.st0{fill:#132F4C;}'),
            (b'.st0{fill:#132F4C;}', b'.st0{fill:url(https://example.invalid/paint);}'),
            (b'<switch>', b'<image href="data:image/png;base64,AAA"/><switch>'),
            (b'<!ENTITY ns_extend "http://ns.adobe.com/Extensibility/1.0/">', b'<!ENTITY ns_extend SYSTEM "file:///etc/passwd">'),
            (b'<!ENTITY ns_extend "http://ns.adobe.com/Extensibility/1.0/">', b'<!ENTITY ns_extend "&ns_ai;">'),
            (b'<switch>', b'<?xml-stylesheet href="https://example.invalid/style"?><switch>'),
        ]
        for before, after in mutations:
            with self.subTest(after=after):
                self.assertIn(before, self.source)
                with self.assertRaises((ValueError, ET.ParseError)):
                    prepare(self.source.replace(before, after, 1))


if __name__ == '__main__':
    unittest.main()
