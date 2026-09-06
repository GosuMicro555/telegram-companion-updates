# v040 Candidate Reconstruction

## Result

This candidate was reconstructed from `work/final-review-b4af019` and corrected to the explorer's v0.3.0 overlay order. Source snapshots were not modified.

## Authoritative Overlay Manifest

The complete ordered file manifest is [`.reconstruction-applied-map.txt`](.reconstruction-applied-map.txt). It contains 93 copies covering 77 final destinations. Later stages replace earlier stages only at the same destination.

| Order | Stage | Source | Purpose |
| ---: | --- | --- | --- |
| Base | final review | `work/final-review-b4af019` | complete 226-file repository base |
| 1 | retained investigation | `work/current-investigation` | 24 files with no v030 replacement |
| 2 | v030 root | `work/v030` | root-level analytics, storage, bindings, frontend, and Wails files |
| 3 | v030 backend | `work/v030/backend` | SQLite and analytics implementation/test variants |
| 4 | v030 frontend | `work/v030/frontend` | explicitly located frontend artifacts |
| 5 | v030 geometry | `work/v030/geometry` | geometry scripts, generated model, and root test |
| 6 | v030 sender fix | `work/v030/sender-fix` | sender implementation and test |
| 7 | v030 localize | `work/v030/localize` | final canonical three-class analytics view, test, and localization |
| 8 | Wails hook | `stage-rereview-wails-hook` | `scripts/package_wails_model.sh` |

The final `wails.json` is from v030 root and declares product version `0.3.0`. The final `frontend/src/AnalyticsView.tsx` and its test are from `v030/localize`; the view uses `neutral`, `positive`, and `negative` canonical classes and does not contain the legacy topic/general/imports workflow.

## Verification

| Check | Result |
| --- | --- |
| Final overlay hashes | 0 mismatches across 77 final destinations |
| Wails product version | `0.3.0` |
| Analytics final source | `07-v030-localize` |
| Legacy analytics symbols (`StartTopicComparison`, `SelectAndImportAIFile`) | absent |
| Wails packaging hook | present at `scripts/package_wails_model.sh` |

## Test Summary

| Command | Result |
| --- | --- |
| `go test ./...` | Not run: `go` is not available on `PATH`. |
| `cd frontend && npm test -- --run` | Not run: `npm` is not available on `PATH`, and `frontend/node_modules` is absent. |

No 0.4.0 feature work was added. Files with no unambiguous repository destination, including the investigation's standalone `tor` package files, remain excluded.
