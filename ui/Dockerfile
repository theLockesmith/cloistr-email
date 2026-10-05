# Build stage
FROM node:22.23.3-alpine AS builder

WORKDIR /app

# Token for the private @cloistr GitLab npm scope (see .npmrc). Injected at
# build time via --build-arg NPM_TOKEN=$CI_JOB_TOKEN; only lives in this
# discarded builder stage, never in the runtime image.
ARG NPM_TOKEN
ENV NPM_TOKEN=${NPM_TOKEN}

COPY package*.json .npmrc ./
RUN npm ci

COPY . .

# Same-origin API in production: nginx proxies /api → backend, and the client
# appends /api/v1 or /api/v2 itself, so this must be EMPTY (a value of "/api"
# yielded the /api/api/... double prefix that 404'd every request).
ARG VITE_API_URL=
ENV VITE_API_URL=${VITE_API_URL}

RUN npm run build

# Runtime stage - use unprivileged nginx for OpenShift compatibility
FROM nginxinc/nginx-unprivileged:1.31-alpine

# Copy app files
COPY --from=builder /app/dist /usr/share/nginx/html

# Server config is a template: the base image's entrypoint renders
# /etc/nginx/templates/*.template into /etc/nginx/conf.d/ at container start,
# which is what lets /config.js carry per-environment service hosts.
COPY nginx.conf.template /etc/nginx/templates/default.conf.template

# Production values as defaults: a deployment that sets nothing serves exactly
# what production served before runtime config existed. Staging overrides these
# at deploy time. The filter is load-bearing: without it envsubst would also
# replace nginx's own $uri, $host and friends.
ENV CLOISTR_RELAY_URL=wss://relay.cloistr.xyz \
    CLOISTR_SIGNER_URL=https://signer.cloistr.xyz \
    CLOISTR_BLOSSOM_URL=https://files.cloistr.xyz \
    CLOISTR_DISCOVERY_URL=https://discover.cloistr.xyz \
    CLOISTR_APP_URL=https://mail.cloistr.xyz \
    CLOISTR_ENVIRONMENT=production \
    CLOISTR_BACKEND_URL=http://cloistr-email-backend \
    NGINX_ENVSUBST_FILTER=^CLOISTR_

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --quiet --tries=1 --spider http://localhost:8080 || exit 1

CMD ["nginx", "-g", "daemon off;"]
