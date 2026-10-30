"""Negative controls for bounded, byte-preserving collector approval regions."""
import sys
import json
import re
import unittest
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'scripts'))
from collector_provenance import restore_reviewed_regions, sha256


class ReviewedRegions(unittest.TestCase):
    def setUp(self):
        self.before = b'protected-before\nold\nprotected-after\n'
        self.after = b'protected-before\nreviewed-new\nprotected-after\n'
        self.region = {'before_start':17, 'before_end':21, 'before_sha256':sha256(b'old\n'),
                       'after_length':13, 'after_sha256':sha256(b'reviewed-new\n'), 'reason':'Synthetic approved change'}

    def test_exact_reviewed_change_restores_original(self):
        self.assertEqual(restore_reviewed_regions(self.after, self.before, [self.region]), self.before)

    def test_protected_prefix_suffix_and_changed_span_reject_mutation(self):
        for data in [b'X'+self.after[1:], self.after[:-1]+b'X', self.after.replace(b'reviewed-new', b'altered-news')]:
            with self.assertRaises(ValueError):
                restore_reviewed_regions(data, self.before, [self.region])

    def test_missing_extra_or_truncated_bytes_are_not_approved(self):
        for data in [self.after+b'X', self.after[:-2], self.after.replace(b'protected-after', b'')]:
            with self.assertRaises(ValueError):
                restore_reviewed_regions(data, self.before, [self.region])

    def test_overlapping_and_out_of_bounds_regions_rejected(self):
        with self.assertRaises(ValueError):
            restore_reviewed_regions(self.after, self.before, [self.region, self.region])
        with self.assertRaises(ValueError):
            restore_reviewed_regions(self.after, self.before, [{**self.region, 'before_end':1000}])

    def test_archived_span_hash_and_reason_required(self):
        for region in [{**self.region, 'before_sha256':'0'*64}, {**self.region, 'reason':''}]:
            with self.assertRaises(ValueError):
                restore_reviewed_regions(self.after, self.before, [region])

    def test_empty_regions_require_identical_bytes(self):
        self.assertEqual(restore_reviewed_regions(self.before, self.before, []), self.before)
        with self.assertRaises(ValueError):
            restore_reviewed_regions(self.after, self.before, [])

class ActualReportRegions(unittest.TestCase):
    def test_current_report_regions_are_narrow_and_reject_changed_bytes(self):
        root = Path(__file__).resolve().parents[1]
        metadata = json.loads((root / 'docs/verification/collector-change-verification.json').read_text())
        report = json.loads((root / 'docs/verification/report-verification.json').read_text())
        renderer = (root / report['original_renderer']['path']).read_bytes()
        def normalize(data):
            data = re.sub(rb"\$scriptVersion = '\d+\.\d+\.\d+'", b"$scriptVersion = '3.0.0'", data, count=1)
            start = data.index(b'function Write-ConsolidatedHtmlReport {')
            end = data.index(b'function Write-DocxTextRun {', start)
            return data[:start] + renderer + data[end:]
        entry = metadata['files']['Export-ServerInventory.ps1']
        before = normalize((root / entry['archive_path']).read_bytes())
        after = normalize((root / 'src/app/Export-ServerInventory.ps1').read_bytes())
        self.assertEqual(restore_reviewed_regions(after, before, entry['regions']), before)
        delta = 0
        checked = 0
        for region in entry['regions']:
            start = region['before_start'] + delta
            if any(term in region['reason'] for term in ('array/list values', 'flat string report cells', 'Word table paragraphs')):
                self.assertLess(region['after_length'], 2000)
                changed = after[:start] + bytes([after[start] ^ 1]) + after[start + 1:]
                with self.assertRaises(ValueError):
                    restore_reviewed_regions(changed, before, entry['regions'])
                checked += 1
            delta += region['after_length'] - (region['before_end'] - region['before_start'])
        self.assertEqual(checked, 3)
        # An unrelated collector remains outside every approval region.
        changed = after.replace(b'function Invoke-NativeCapture {', b'function Invoke-NativeCapturX {', 1)
        self.assertNotEqual(changed, after)
        with self.assertRaises(ValueError):
            restore_reviewed_regions(changed, before, entry['regions'])


if __name__ == '__main__':
    unittest.main()
