# iOS refresh cleanup verification

## Scope
- iOS only. Removed Activities toolbar sync icon, feed sync/refresh rows, Profile local reload control and refresh prompt. No metric or activity-date removals.
- Pull-to-refresh and existing automatic reads retained; no automatic Strava POST. Explicit upstream import and actionable import diagnostics remain in Profile.
- Fixed reproduced pull-task cancellation and same-year cache loss/overlapping reads.
- Original worktree `/Users/tinyserve/Projects/weirdstats` has unrelated icon/design changes; implementation uses isolated `weirdstats-refresh-cleanup`. Bundle/team/project/signing/workflow files unchanged.

## Local evidence
- RED: `weirdstats-refresh-red.xcresult` failed on old standalone controls; `weirdstats-history-red2.xcresult` failed cache/deduping assertions.
- Real delayed pull initially produced `cancelled` and no new HTTP request on Activities (`weirdstats-pull-diagnostic.xcresult`). Awaited independent task passed (`weirdstats-pull-isolation.xcresult`).
- Full `weirdstats-refresh-full.xcresult`: **35 passed, 0 failed/skipped**, Xcode 27.2, iOS 27.2 iPhone 18 Pro, isolated simulator `E80A2539-8E40-4876-880E-3B23CBC9C14C`.
- Four-second loopback delays, exact GET counts on all four actual pull gestures; held URLProtocol cache/deduping/error/session tests. Fixtures only, no real Strava account or sync.
- Python distribution/presentation contracts: **8 passed**. `bash -n`, `git diff --check`: passed. Unsigned Release device archive passed.
- Independent read-only Codex review: passed, no security or logic findings. Evidence: `~/.hermes/cache/scratch/weirdstats-refresh-review.txt`.
- Exported screenshot directory: `~/.hermes/cache/scratch/weirdstats-refresh-evidence`; inspected Activities, Facts, Profile, Year and retained offline feed. Calendar remains scrollable beneath floating tabs; existing December/large-type hit-target tests passed.
- The gesture harness waits for XCTest idle, so screenshots are completed-state evidence; delayed transport and model tests establish pending behavior rather than claiming the screenshots show an in-flight spinner.

## Release
Pending publication. Apple processing, tester availability and account-backed Strava verification are separate from successful upload.

## Vault
The requested existing `Apps/WeirdStats.md` was not found in the configured iCloud `TheLog` vault (Apps directory inspected). No replacement or duplicate note created.
