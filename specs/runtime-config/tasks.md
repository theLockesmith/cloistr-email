# Tasks

- [x] Shared reader for the signer at every call site (req 2). Evidence: grep for cloistr.xyz URL literals in ui/src returns none; Header, LoginModal, BackendAuthProvider all pass SIGNER_URL (reviewer confirmed, 2026-10-05).
- [x] nginx template + exact-match no-store /config.js (req 1). Evidence: curl of /config.js on both local containers returned "Cache-Control: no-store" with production vs staging bodies from one image id (2026-10-05).
- [x] Production ENV defaults in the image (req 3). Evidence: container run with no env served environment "production" and signer.cloistr.xyz; random-uid run (1001140000:0) rendered the template (2026-10-05).
- [x] Browser proof: staging hostname requests only staging hosts (req 4). Evidence: Chromium on mail.staging.cloistr.xyz mapped to the staging container requested only https://signer.staging.cloistr.xyz; production run requested https://signer.cloistr.xyz (2026-10-05).
- [x] Display-safe SIGNER_HOST, tested (req 5). Evidence: serviceConfig.test.ts malformed-value case failed before the fix, passes after; vitest 77/77 (2026-10-05).
- [x] Template the /api proxy target (req 6). Evidence: CLOISTR_BACKEND_URL in template with previous host as image default. Not yet re-run in a container.
- [ ] Rebuild and re-run the two-container proof after the template change
- [ ] Merge, deploy, confirm live config.js reports production
