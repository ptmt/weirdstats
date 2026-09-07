import copy
import datetime
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from signing_metadata import BUNDLE_ID, export_options


class SigningMetadataTests(unittest.TestCase):
    def setUp(self):
        self.profile = {
            "UUID": "12345678-1234-1234-1234-123456789ABC",
            "TeamIdentifier": ["REVIEWTEAM"],
            "ApplicationIdentifierPrefix": ["REVIEWTEAM"],
            "ExpirationDate": datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(days=30),
            "Entitlements": {
                "application-identifier": f"REVIEWTEAM.{BUNDLE_ID}",
                "get-task-allow": False,
            },
        }

    def test_upload_preserves_ci_build_number_and_uses_exact_profile(self):
        options = export_options(self.profile, "REVIEWTEAM")
        self.assertEqual(options["destination"], "upload")
        self.assertEqual(options["method"], "app-store-connect")
        self.assertFalse(options["manageAppVersionAndBuildNumber"])
        self.assertEqual(options["provisioningProfiles"], {BUNDLE_ID: self.profile["UUID"]})

    def test_legacy_app_id_prefix_is_supported(self):
        self.profile["ApplicationIdentifierPrefix"] = ["OLDPREFIX"]
        self.profile["Entitlements"]["application-identifier"] = f"OLDPREFIX.{BUNDLE_ID}"
        export_options(self.profile, "REVIEWTEAM")

    def test_rejects_wrong_team(self):
        with self.assertRaisesRegex(ValueError, "APPLE_TEAM_ID"):
            export_options(self.profile, "OTHERTEAM")

    def test_rejects_wrong_app_and_wildcard(self):
        for app_id in ["REVIEWTEAM.com.other.app", "REVIEWTEAM.*"]:
            with self.subTest(app_id=app_id), self.assertRaisesRegex(ValueError, "explicitly match"):
                self.profile["Entitlements"]["application-identifier"] = app_id
                export_options(self.profile, "REVIEWTEAM")

    def test_rejects_development_ad_hoc_and_enterprise(self):
        profiles = [copy.deepcopy(self.profile) for _ in range(3)]
        profiles[0]["Entitlements"]["get-task-allow"] = True
        profiles[1]["ProvisionedDevices"] = ["some-device"]
        profiles[2]["ProvisionsAllDevices"] = True
        for profile in profiles:
            with self.subTest(profile=profile), self.assertRaisesRegex(ValueError, "distribution profile"):
                export_options(profile, "REVIEWTEAM")

    def test_rejects_expired_profile(self):
        self.profile["ExpirationDate"] = datetime.datetime(2020, 1, 1)
        with self.assertRaisesRegex(ValueError, "expired"):
            export_options(self.profile, "REVIEWTEAM")

    def test_rejects_unsafe_uuid(self):
        self.profile["UUID"] = "../../other-file"
        with self.assertRaisesRegex(ValueError, "UUID"):
            export_options(self.profile, "REVIEWTEAM")


if __name__ == "__main__":
    unittest.main()
