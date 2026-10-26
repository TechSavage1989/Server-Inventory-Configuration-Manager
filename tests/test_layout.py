"""Layout guard positives and root-clutter negative controls."""
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('layout', Path(__file__).resolve().parents[1] / 'scripts/check_layout.py')
layout = importlib.util.module_from_spec(spec)
spec.loader.exec_module(layout)

class LayoutTests(unittest.TestCase):
    def test_existing_structure(self):
        self.assertEqual([], layout.violations(['README.md', 'src/app/main.go', 'docs/release-notes/3.2.9.md']))
    def test_root_clutter(self):
        self.assertEqual(['debug.log', 'main.go'], layout.violations(['main.go', 'debug.log']))
    def test_tracked_generated_outputs(self):
        self.assertEqual(['build/app.exe', 'dist/source.zip'], layout.violations(['build/app.exe', 'dist/source.zip']))
    def test_new_directory_needs_decision(self):
        self.assertEqual(['misc/scratch.txt'], layout.violations(['misc/scratch.txt']))

if __name__ == '__main__':
    unittest.main()
