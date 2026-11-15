"""Keep secondary loading labels out of already-populated native screens."""
import pathlib
import unittest


class RefreshPresentationTests(unittest.TestCase):
    def test_loading_labels_are_initial_load_only(self):
        source = (pathlib.Path(__file__).resolve().parents[2] / "WeirdStats/ContentView.swift").read_text()
        self.assertIn('if model.isLoadingHistory && model.history?.year != year', source)
        self.assertIn('if model.isLoading && model.activities.isEmpty', source)
        self.assertNotIn('CompactRefreshStatus', source)
        self.assertNotIn('Button("Reload profile and imported activities")', source)
        self.assertNotIn('ProgressView("Refreshing…")', source)
