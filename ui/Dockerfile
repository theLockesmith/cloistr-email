# Build stage
FROM node:20-alpine AS builder

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
FROM nginxinc/nginx-unprivileged:1.25-alpine

# Copy app files
COPY --from=builder /app/dist /usr/share/nginx/html

# Create nginx config for SPA with unprivileged port
USER root
RUN echo 'server { \
    listen 8080; \
    root /usr/share/nginx/html; \
    index index.html; \
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
