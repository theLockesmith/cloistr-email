# Kubernetes Deployment

This directory contains Kubernetes manifests for deploying Cloistr Email.

## Prerequisites

- Kubernetes cluster (1.25+)
- kubectl configured
- cert-manager installed (for TLS)
- nginx ingress controller
- Prometheus Operator (for monitoring)

## Quick Deploy

```bash
# Create namespace and deploy all resources
kubectl apply -k k8s/

# Or apply individually
kubectl apply -f k8s/namespace.yaml
kubectl apply -f k8s/configmap.yaml
kubectl apply -f k8s/secret.yaml  # Edit first!
kubectl apply -f k8s/postgres.yaml
kubectl apply -f k8s/redis.yaml
kubectl apply -f k8s/backend-deployment.yaml
kubectl apply -f k8s/frontend-deployment.yaml
kubectl apply -f k8s/services.yaml
kubectl apply -f k8s/ingress.yaml
kubectl apply -f k8s/hpa.yaml
kubectl apply -f k8s/monitoring.yaml
```

## Configuration

### 1. Edit Secrets

**Never commit real secrets!** Edit `secret.yaml` or create from env file:

```bash
# Create secrets from .env.production
kubectl create secret generic cloistr-email-secrets \
    --from-env-file=.env.production \
    -n coldforge
```

### 2. Generate DKIM Keys

```bash
./scripts/generate-dkim-keys.sh -d coldforge.xyz -s mail
```

Add the private key to your secrets.

### 3. Configure DNS

See [DNS-SETUP.md](../docs/DNS-SETUP.md) for required DNS records.

### 4. Configure TLS

Install cert-manager ClusterIssuer:

```bash
kubectl apply -f - <<EOF
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: admin@cloistr.xyz
    privateKeySecretRef:
      name: letsencrypt-prod-account-key
    solvers:
    - http01:
        ingress:
          class: nginx
EOF
```

## Files

| File | Description |
|------|-------------|
| `namespace.yaml` | Namespace definition |
| `configmap.yaml` | Non-sensitive configuration |
| `secret.yaml` | Sensitive configuration (template) |
| `postgres.yaml` | PostgreSQL StatefulSet |
| `redis.yaml` | Redis Deployment |
| `backend-deployment.yaml` | Backend API Deployment |
| `frontend-deployment.yaml` | Frontend UI Deployment |
| `services.yaml` | Service definitions |
| `ingress.yaml` | Ingress + Certificate |
| `hpa.yaml` | Autoscaling + PodDisruptionBudget |
| `monitoring.yaml` | ServiceMonitor + PrometheusRules |
| `kustomization.yaml` | Kustomize configuration |

## Verify Deployment

```bash
# Check pods
kubectl get pods -n coldforge

# Check services
kubectl get svc -n coldforge

# Check ingress
kubectl get ingress -n coldforge

# Check certificates
kubectl get certificate -n coldforge

# View logs
kubectl logs -f deployment/cloistr-email-backend -n coldforge

# Check health
kubectl exec -it deployment/cloistr-email-backend -n coldforge -- curl localhost:8080/health
```

## Database Initialization

Apply schema on first deployment:

```bash
# Copy schema file into pod
kubectl cp configs/schema.sql coldforge/cloistr-email-postgres-0:/tmp/schema.sql

# Execute schema
kubectl exec -it cloistr-email-postgres-0 -n coldforge -- \
    psql -U email_user -d cloistr_email -f /tmp/schema.sql
```

## Scaling

Manual scaling:
```bash
kubectl scale deployment cloistr-email-backend --replicas=5 -n coldforge
```

HPA handles automatic scaling based on CPU/memory.

## Monitoring

Metrics available at:
- `/metrics` on port 9090

Grafana dashboards import from `configs/grafana/`.

Alerts defined in `monitoring.yaml`.

## Troubleshooting

### Pods not starting

```bash
kubectl describe pod <pod-name> -n coldforge
kubectl logs <pod-name> -n coldforge --previous
```

### Database connection issues

```bash
# Test connection from backend pod
kubectl exec -it deployment/cloistr-email-backend -n coldforge -- \
    sh -c 'nc -zv cloistr-email-postgres 5432'
```

### Certificate issues

```bash
kubectl describe certificate cloistr-email-tls -n coldforge
kubectl describe certificaterequest -n coldforge
kubectl logs -l app=cert-manager -n cert-manager
```

### SMTP not receiving mail

Check LoadBalancer external IP:
```bash
kubectl get svc cloistr-email-smtp -n coldforge
```

Ensure DNS MX record points to this IP.

## Production Checklist

- [ ] Secrets configured (not using defaults)
- [ ] DKIM keys generated and DNS record added
- [ ] SPF record configured
- [ ] DMARC record configured
- [ ] PTR (reverse DNS) configured
- [ ] TLS certificate issued
- [ ] Database schema applied
- [ ] Backups configured
- [ ] Monitoring/alerting set up
- [ ] Load balancer external IP assigned
- [ ] MX record pointing to load balancer
