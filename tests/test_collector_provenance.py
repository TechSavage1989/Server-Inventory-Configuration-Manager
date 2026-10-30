"""Negative controls for bounded, byte-preserving collector approval regions."""
import sys
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

if __name__ == '__main__':
    unittest.main()
