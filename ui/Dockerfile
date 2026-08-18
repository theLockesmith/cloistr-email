# Build stage
FROM node:22.23.2-alpine AS builder

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

# Create nginx config for SPA with unprivileged port
USER root
# email.cloistr.xyz is an ALIAS; mail.cloistr.xyz is canonical. Both Routes
# pointed at this same frontend, so the app served on two origins at once.
# That is not cosmetic: cookies and the shared .cloistr.xyz SSO session are
# per-origin, so a user who landed on email.* had a different browser origin
# from every other app in the suite and could appear signed out.
#
# Redirect here rather than by deleting the Route: the Route keeps its TLS
# certificate and DNS, and anything already linking to email.* keeps working
# instead of breaking. Only the UI is redirected — /api is deliberately left
# alone so existing API clients on email.* are not broken by a 301 they may
# not follow on non-GET verbs.
RUN echo 'server { \
    listen 8080; \
    root /usr/share/nginx/html; \
    index index.html; \
    if ($host = email.cloistr.xyz) { \
        return 301 https://mail.cloistr.xyz$request_uri; \
    } \
    location / { \
        try_files $uri $uri/ /index.html; \
    } \
    location /api { \
        resolver 127.0.0.11 valid=30s; \
        set $backend http://cloistr-email-backend; \
        proxy_pass $backend; \
        proxy_set_header Host $host; \
        proxy_set_header X-Real-IP $remote_addr; \
    } \
}' > /etc/nginx/conf.d/default.conf && \
    chown -R nginx:nginx /usr/share/nginx/html
USER nginx

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --quiet --tries=1 --spider http://localhost:8080 || exit 1

CMD ["nginx", "-g", "daemon off;"]
