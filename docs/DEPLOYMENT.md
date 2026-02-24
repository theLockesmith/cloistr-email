# Cloistr Email Deployment Guide

## Local Development

### Prerequisites

- Docker and Docker Compose
- Git
- Go 1.21+ (for local testing)
- Node.js 20+ (for UI development)

### Quick Start

```bash
# Clone repository
git clone git@gitlab-coldforge:coldforge/cloistr-email.git
cd cloistr-email

# Start all services
docker-compose up

# Backend API: http://localhost:8080
# Frontend UI: http://localhost:3001
# Stalwart Admin: http://localhost:6001
```

### Local Testing

```bash
# Run unit tests
go test -v ./...

# Run with coverage
go test -v -cover ./...

# Run integration tests (requires docker-compose up)
go test -v -tags=integration ./tests/integration/...

# Test UI
cd ui
npm install
npm test
```

## Staging Deployment

### Prerequisites

- Kubernetes cluster (Atlantis)
- Atlas roles deployed
- GitLab with CI/CD enabled

### Deploy via CI/CD

```bash
# Push to develop branch
git push origin develop

# GitLab CI automatically:
# 1. Runs tests
# 2. Builds Docker image
# 3. Pushes to registry

# For manual deployment:
# Open CI/CD pipeline in GitLab
# Click "deploy:atlas" manual job
# Select "staging" environment
```

### Configure Stalwart for Staging

1. SSH into Stalwart pod
2. Configure virtual domain:

```toml
[domains.staging]
hostname = "mail.staging.coldforge.xyz"
```

3. Restart Stalwart

### Set Up NIP-05 Discovery

Create `.well-known/nostr.json` endpoint:

```json
{
  "names": {
    "alice": "npub1alice...",
    "bob": "npub1bob..."
  },
  "relays": {
    "npub1...": ["wss://relay.coldforge.xyz"]
  }
}
```

## Production Deployment

### Prerequisites

- Kubernetes cluster (Atlantis)
- Production domain (mail.coldforge.xyz)
- TLS certificates
- Backup strategy for PostgreSQL
- Monitoring setup

### Deployment Steps

1. **Create Atlas role:**
   ```
   ~/Atlas/roles/kube/cloistr-email/
   ```

2. **Configure Kubernetes manifests:**
   - Deployment (backend + frontend)
   - Service
   - ConfigMap (settings)
   - Secret (credentials)
   - PersistentVolumeClaim (storage)

3. **Configure ingress:**
   ```yaml
   apiVersion: networking.k8s.io/v1
   kind: Ingress
   metadata:
     name: cloistr-email
   spec:
     rules:
     - host: mail.coldforge.xyz
       http:
         paths:
         - path: /
           backend:
             service:
               name: cloistr-email
               port:
                 number: 3001  # Frontend
         - path: /api
           backend:
             service:
               name: cloistr-email-api
               port:
                 number: 8080  # Backend
   ```

4. **Initialize database:**
   ```bash
   kubectl exec -it cloistr-email-backend -- ./cloistr-email migrate
   ```

5. **Configure Stalwart:**
   - Set up virtual domain for coldforge.xyz
   - Configure TLS certificates
   - Set up SPF/DKIM/DMARC records

6. **Configure NIP-05 Discovery:**
   - Serve `.well-known/nostr.json` at coldforge.xyz
   - Map users@coldforge.xyz to their npubs

7. **Verify deployment:**
   ```bash
   # Check services
   kubectl get svc -n coldforge

   # Check pods
   kubectl get pods -n coldforge

   # View logs
   kubectl logs -f deployment/cloistr-email -n coldforge
   ```

### Domain Configuration

**DNS Records:**

```
mail.coldforge.xyz A -> <k8s-ip>
coldforge.xyz A -> <k8s-ip>

# Mail server
coldforge.xyz MX 10 mail.coldforge.xyz

# Authentication
coldforge.xyz SPF "v=spf1 mx ~all"
coldforge.xyz TXT "v=DKIM1; ..."

# TLS/DNSSEC
coldforge.xyz TLSA 3 1 1 <cert-hash>
```

**TLS Certificates:**

Use Let's Encrypt or similar:
```bash
certbot certonly -d mail.coldforge.xyz -d coldforge.xyz
```

### Backup & Recovery

**PostgreSQL Backups:**

```bash
# Automated daily backups
kubectl exec cloistr-email-postgres -- pg_dump -U postgres cloistr_email | gzip > backup.sql.gz

# Restore
gunzip -c backup.sql.gz | kubectl exec -i cloistr-email-postgres -- psql -U postgres
```

**Email Storage:**

Email bodies are encrypted and stored in PostgreSQL. Backing up the database backs up all emails.

## Monitoring & Logging

### Logging

Logs are sent to stdout and available via:

```bash
# View backend logs
kubectl logs -f deployment/cloistr-email-backend -n coldforge

# View frontend logs
kubectl logs -f deployment/cloistr-email-frontend -n coldforge

# Stream all logs
kubectl logs -f -l app=cloistr-email -n coldforge
```

### Metrics

Prometheus metrics available at:
```
http://cloistr-email-api:9090/metrics
```

**Available metrics:**

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `cloistr_email_emails_sent_total` | Counter | transport, encrypted, status | Total emails sent |
| `cloistr_email_emails_received_total` | Counter | transport, verified | Total emails received |
| `cloistr_email_email_send_duration_seconds` | Histogram | transport | Email send latency |
| `cloistr_email_nostr_signatures_total` | Counter | operation, result | Nostr signature operations |
| `cloistr_email_nostr_verifications_total` | Counter | result | Email signature verifications |
| `cloistr_email_encryption_operations_total` | Counter | operation, mode, result | Encryption operations |
| `cloistr_email_nip05_lookups_total` | Counter | result | NIP-05 lookups (success/failure/cached) |
| `cloistr_email_nip05_lookup_duration_seconds` | Histogram | - | NIP-05 lookup latency |
| `cloistr_email_nip05_cache_size` | Gauge | - | Current NIP-05 cache entries |
| `cloistr_email_auth_attempts_total` | Counter | method, result | Authentication attempts |
| `cloistr_email_active_sessions` | Gauge | - | Current active sessions |
| `cloistr_email_http_requests_total` | Counter | method, path, status | HTTP requests |
| `cloistr_email_http_request_duration_seconds` | Histogram | method, path | HTTP request latency |
| `cloistr_email_db_query_duration_seconds` | Histogram | operation | Database query latency |
| `cloistr_email_smtp_connections_total` | Counter | direction, result | SMTP connection attempts |
| `cloistr_email_lightning_payments_total` | Counter | result | Lightning payments (future) |

### Health Checks

```bash
# Service health
curl http://cloistr-email:8080/health

# Readiness check
curl http://cloistr-email:8080/ready
```

## Troubleshooting

### Email Not Sending

1. Check Stalwart logs:
   ```bash
   kubectl logs -f statefulset/stalwart-mail -n coldforge
   ```

2. Verify SMTP connectivity:
   ```bash
   telnet mail.coldforge.xyz 587
   ```

3. Check SPF/DKIM records:
   ```bash
   dig coldforge.xyz MX
   dig coldforge.xyz TXT
   ```

### Decryption Failing

1. Verify nsecbunker is reachable:
   ```bash
   curl -w "\n" wss://nsecbunker.coldforge.xyz
   ```

2. Check NIP-46 challenge storage:
   ```bash
   kubectl exec -it redis -- redis-cli
   > KEYS "nip46:challenge:*"
   ```

### Database Issues

1. Connect to PostgreSQL:
   ```bash
   kubectl exec -it cloistr-email-postgres -- psql -U email_user cloistr_email
   ```

2. Check schema:
   ```sql
   \dt  -- List tables
   SELECT COUNT(*) FROM emails;  -- Check email count
   ```

## Rollback Procedure

```bash
# View deployment history
kubectl rollout history deployment/cloistr-email-backend -n coldforge

# Rollback to previous version
kubectl rollout undo deployment/cloistr-email-backend -n coldforge

# Rollback to specific revision
kubectl rollout undo deployment/cloistr-email-backend --to-revision=2 -n coldforge
```

## Performance Tuning

### Database Optimization

```sql
-- Add indexes for common queries
CREATE INDEX idx_emails_user_created ON emails(user_id, created_at DESC);
CREATE INDEX idx_emails_status_folder ON emails(status, folder);

-- Vacuum and analyze
VACUUM ANALYZE;
```

### Caching Strategy

- NIP-05 lookups cached for 24 hours
- Session data cached in Redis with 24-hour TTL
- Contact lookups cached for 1 hour

### Load Balancing

- Use Kubernetes Service with multiple replicas
- Configure HPA for auto-scaling:

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: cloistr-email
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: cloistr-email-backend
  minReplicas: 2
  maxReplicas: 10
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 70
```

## See Also

- [README.md](../README.md) - Project overview
- [API.md](./API.md) - API documentation
- [ARCHITECTURE.md](./ARCHITECTURE.md) - System architecture
- [ENCRYPTION.md](./ENCRYPTION.md) - Encryption design
