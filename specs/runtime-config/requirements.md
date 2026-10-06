# Runtime service config: cloistr-email frontend

Adoption of the shared mechanism specified in cloistr-collab-common
`specs/runtime-config/` (recipe: `docs/runtime-config-adoption.md`). Staging
definition: `architecture/staging-environment.md` in the Cloistr docs tree.

1. One frontend image serves production and staging; hosts are read at
   container start, never baked per environment.
2. Every production hostname the UI uses goes through
   `@cloistr/collab-common/config`, including hosts passed to shared
   components (`Header`, `LoginModal`, `BackendAuthProvider`).
3. A deployment that sets nothing behaves exactly as production did before.
4. Under staging values, the browser requests no production `*.cloistr.xyz`
   host.
5. A malformed runtime value fails visibly; it never blanks the page and
   never falls back to production.
6. The nginx proxy target for `/api` is configurable per environment.
