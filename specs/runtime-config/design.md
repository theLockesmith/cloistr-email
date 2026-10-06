# Design

- `ui/src/lib/serviceConfig.ts` resolves the config once at module load and
  exports `SIGNER_URL` and a display-safe `SIGNER_HOST`. All signer uses
  import from it.
- `ui/index.html` loads `/config.js` with a classic script before the module
  bundle.
- `ui/nginx.conf.template` is rendered by the nginx-unprivileged entrypoint
  (envsubst, `NGINX_ENVSUBST_FILTER=^CLOISTR_`). `location = /config.js`
  (exact match, `no-store`) emits `window.__CLOISTR_CONFIG__`. The `/api`
  proxy target is `${CLOISTR_BACKEND_URL}`.
- `ui/Dockerfile` sets production values as ENV defaults, including
  `CLOISTR_BACKEND_URL=http://cloistr-email-backend`.
