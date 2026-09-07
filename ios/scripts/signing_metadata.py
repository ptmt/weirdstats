"""Validate an App Store profile and create xcodebuild's upload options."""

import datetime
import plistlib
import re
import sys
from pathlib import Path

BUNDLE_ID = "com.ptmt.weirdstats"


def export_options(profile, team_id):
    if profile.get("TeamIdentifier") != [team_id]:
        raise ValueError("Provisioning profile does not match APPLE_TEAM_ID")
    prefixes = profile.get("ApplicationIdentifierPrefix", [])
    entitlement = profile.get("Entitlements", {})
    app_id = entitlement.get("application-identifier")
    if not prefixes or app_id not in [f"{prefix}.{BUNDLE_ID}" for prefix in prefixes]:
        raise ValueError(f"Provisioning profile must explicitly match {BUNDLE_ID}")
    if ("ProvisionedDevices" in profile or profile.get("ProvisionsAllDevices")
            or entitlement.get("get-task-allow", False)):
        raise ValueError("Use an App Store Connect distribution profile, not a development/ad hoc profile")
    expiry = profile.get("ExpirationDate")
    if not isinstance(expiry, datetime.datetime) or expiry.replace(tzinfo=datetime.timezone.utc) <= datetime.datetime.now(datetime.timezone.utc):
        raise ValueError("Provisioning profile has expired or has no expiration date")
    uuid = profile.get("UUID", "")
    if not re.fullmatch(r"[0-9A-Fa-f]{8}(?:-[0-9A-Fa-f]{4}){3}-[0-9A-Fa-f]{12}", uuid):
        raise ValueError("Provisioning profile UUID is invalid")
    return {
        "method": "app-store-connect",
        "destination": "upload",
        "teamID": team_id,
        "signingStyle": "manual",
        "signingCertificate": "Apple Distribution",
        "provisioningProfiles": {BUNDLE_ID: uuid},
        "manageAppVersionAndBuildNumber": False,
        "uploadSymbols": True,
    }


if __name__ == "__main__":
    try:
        profile = plistlib.loads(Path(sys.argv[1]).read_bytes())
        options = export_options(profile, sys.argv[2])
        Path(sys.argv[3]).write_bytes(plistlib.dumps(options))
        print(options["provisioningProfiles"][BUNDLE_ID])
    except (ValueError, KeyError, plistlib.InvalidFileException) as error:
        sys.exit(str(error))
