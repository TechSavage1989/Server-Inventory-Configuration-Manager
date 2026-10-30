"""Guard UTF-8 renderer inputs and Windows PowerShell 5.1 test fixtures."""
import importlib.util
import unittest
from pathlib import Path
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('embed_report', ROOT / 'scripts/embed_report_renderer.py')
embed_report = importlib.util.module_from_spec(spec)
spec.loader.exec_module(embed_report)

class ReportEncoding(unittest.TestCase):
    def test_renderer_ignores_platform_default_encoding(self):
        read_text = Path.read_text
        def windows_default(path, encoding=None, errors=None):
            return read_text(path, encoding=encoding or 'cp1252', errors=errors)
        expected = embed_report.renderer()
        with patch.object(Path, 'read_text', windows_default):
            self.assertEqual(embed_report.renderer(), expected)
        css = (ROOT / 'reporting/report.css').read_bytes()
        # Negative control: platform-default Windows decoding corrupts sort arrows.
        self.assertNotEqual(css.decode('cp1252'), css.decode('utf-8'))

    def test_unicode_powershell_fixtures_have_utf8_bom(self):
        for name in ('Test-Report.ps1', 'Test-ReportValues.ps1',
                     'Test-ReportPerformance.ps1', 'Measure-ReportPerformance.ps1'):
            with self.subTest(name=name):
                data = (ROOT / 'scripts' / name).read_bytes()
                self.assertTrue(data.startswith(b'\xef\xbb\xbf'))
                data.decode('utf-8-sig')

if __name__ == '__main__':
    unittest.main()
