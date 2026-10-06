# Tasks

- [x] Shared reader for the signer at every call site (req 2) (verified grep for cloistr.xyz URL literals in ui/src returns none; Header, LoginModal, BackendAuthProvider all pass SIGNER_URL (reviewer confirmed, 2026-10-05).)
- [x] nginx template + exact-match no-store /config.js (req 1) (verified curl of /config.js on both local containers returned "Cache-Control: no-store" with production vs staging bodies from one image id (2026-10-05).)
- [x] Production ENV defaults in the image (req 3) (verified container run with no env served environment "production" and signer.cloistr.xyz; random-uid run (1001140000:0) rendered the template (2026-10-05).)
- [x] Browser proof: staging hostname requests only staging hosts (req 4) (verified Chromium on mail.staging.cloistr.xyz mapped to the staging container requested only https://signer.staging.cloistr.xyz; production run requested https://signer.cloistr.xyz (2026-10-05).)
- [x] Display-safe SIGNER_HOST, tested (req 5) (verified serviceConfig.test.ts malformed-value case failed before the fix, passes after; vitest 77/77 (2026-10-05).)
- [x] Template the /api proxy target (req 6) (verified 2026-10-06: rendered config in the no-env container reads "set $backend http://cloistr-email-backend;", the staging-env container reads the staging service; $host survives envsubst)
- [x] Rebuild and re-run the two-container proof after the template change (verified 2026-10-06: one image id run twice; no-store on both; Chromium on the staging hostname requested only signer.staging.cloistr.xyz, production run requested signer.cloistr.xyz)
- [ ] Merge, deploy, confirm live config.js reports production
