#!/bin/bash
set -euo pipefail

# Secrets enter only through environment variables; never enable shell tracing.
required=(
  APPLE_TEAM_ID APPLE_DISTRIBUTION_CERTIFICATE_BASE64
  APPLE_PROVISIONING_PROFILE_BASE64 APP_STORE_CONNECT_KEY_ID
  APP_STORE_CONNECT_ISSUER_ID APP_STORE_CONNECT_PRIVATE_KEY
  RUNNER_TEMP GITHUB_RUN_NUMBER GITHUB_RUN_ATTEMPT
)
for name in "${required[@]}"; do
  if [[ -z "${!name:-}" ]]; then
    echo "::error::Missing $name. See ios/README.md for TestFlight setup."
    exit 1
  fi
done
# Empty PKCS#12 passwords are supported, but the variable must be present.
: "${APPLE_DISTRIBUTION_CERTIFICATE_PASSWORD?Set the certificate password (may be empty)}"

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$repo_root"
build_number="$GITHUB_RUN_NUMBER.$GITHUB_RUN_ATTEMPT"
set --
if [[ "${GITHUB_REF:-}" == refs/tags/ios/v* ]]; then
  version="${GITHUB_REF#refs/tags/ios/v}"
  if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo '::error::Release tags must use ios/vMAJOR.MINOR.PATCH (for example ios/v0.1.0).'
    exit 1
  fi
  set -- "MARKETING_VERSION=$version"
fi

umask 077
signing_dir="$(mktemp -d "$RUNNER_TEMP/weirdstats-signing.XXXXXX")"
keychain="$signing_dir/signing.keychain-db"
profile_uuid=""
cleanup() {
  security delete-keychain "$keychain" >/dev/null 2>&1 || true
  if [[ -n "$profile_uuid" ]]; then
    rm -f "$HOME/Library/MobileDevice/Provisioning Profiles/$profile_uuid.mobileprovision"
    rm -f "$HOME/Library/Developer/Xcode/UserData/Provisioning Profiles/$profile_uuid.mobileprovision"
  fi
  rm -rf "$signing_dir"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

printf '%s' "$APPLE_DISTRIBUTION_CERTIFICATE_BASE64" | base64 --decode > "$signing_dir/certificate.p12"
printf '%s' "$APPLE_PROVISIONING_PROFILE_BASE64" | base64 --decode > "$signing_dir/profile.mobileprovision"
printf '%s' "$APP_STORE_CONNECT_PRIVATE_KEY" > "$signing_dir/AuthKey.p8"
openssl pkey -in "$signing_dir/AuthKey.p8" -noout >/dev/null
security cms -D -i "$signing_dir/profile.mobileprovision" > "$signing_dir/profile.plist"
profile_uuid="$(python3 ios/scripts/signing_metadata.py \
  "$signing_dir/profile.plist" "$APPLE_TEAM_ID" "$signing_dir/ExportOptions.plist")"

keychain_password="$(openssl rand -hex 32)"
security create-keychain -p "$keychain_password" "$keychain"
security set-keychain-settings -lut 21600 "$keychain"
security unlock-keychain -p "$keychain_password" "$keychain"
security import "$signing_dir/certificate.p12" -P "$APPLE_DISTRIBUTION_CERTIFICATE_PASSWORD" \
  -A -t cert -f pkcs12 -k "$keychain"
security set-key-partition-list -S apple-tool:,apple:,codesign: -k "$keychain_password" "$keychain" >/dev/null
security list-keychains -d user -s "$keychain"
if ! security find-identity -v -p codesigning "$keychain" | grep -q 'Apple Distribution:'; then
  echo '::error::The P12 must contain a valid Apple Distribution certificate and its private key.'
  exit 1
fi

# Support both provisioning-profile locations used by Xcode versions.
for profile_dir in \
  "$HOME/Library/MobileDevice/Provisioning Profiles" \
  "$HOME/Library/Developer/Xcode/UserData/Provisioning Profiles"; do
  mkdir -p "$profile_dir"
  cp "$signing_dir/profile.mobileprovision" "$profile_dir/$profile_uuid.mobileprovision"
done

archive_path="$RUNNER_TEMP/WeirdStats.xcarchive"
xcodebuild -project ios/WeirdStats.xcodeproj -scheme WeirdStats \
  -configuration Release -destination 'generic/platform=iOS' \
  -archivePath "$archive_path" \
  DEVELOPMENT_TEAM="$APPLE_TEAM_ID" CODE_SIGN_STYLE=Manual \
  CODE_SIGN_IDENTITY='Apple Distribution' PROVISIONING_PROFILE_SPECIFIER="$profile_uuid" \
  CURRENT_PROJECT_VERSION="$build_number" "$@" archive

xcodebuild -exportArchive -archivePath "$archive_path" \
  -exportOptionsPlist "$signing_dir/ExportOptions.plist" \
  -exportPath "$RUNNER_TEMP/weirdstats-export" \
  -allowProvisioningUpdates \
  -authenticationKeyPath "$signing_dir/AuthKey.p8" \
  -authenticationKeyID "$APP_STORE_CONNECT_KEY_ID" \
  -authenticationKeyIssuerID "$APP_STORE_CONNECT_ISSUER_ID"

if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  printf 'Uploaded WeirdStats build **%s** to App Store Connect. It will appear in TestFlight after Apple finishes processing.\n' \
    "$build_number" >> "$GITHUB_STEP_SUMMARY"
fi
