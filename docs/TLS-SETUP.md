# TLS Certificate Setup for Cloistr Email

This guide covers TLS certificate configuration for SMTP and HTTPS.

## Overview

Cloistr Email requires TLS certificates for:
1. **SMTP Inbound** (port 25/465) - Receiving encrypted email
2. **SMTP Outbound** (port 587) - Sending via STARTTLS
3. **HTTPS API** (port 443) - Web interface and API

## Option 1: Let's Encrypt with Certbot (Recommended)

### Install Certbot

```bash
# Debian/Ubuntu
sudo apt update
sudo apt install certbot

# RHEL/CentOS
sudo dnf install certbot

# macOS
brew install certbot
```

### Generate Certificates

**Standalone mode (stops other services temporarily):**
```bash
sudo certbot certonly --standalone \
    -d mail.coldforge.xyz \
    -d coldforge.xyz \
    --email admin@coldforge.xyz \
    --agree-tos
```

**Webroot mode (requires running web server):**
```bash
sudo certbot certonly --webroot \
    -w /var/www/html \
    -d mail.coldforge.xyz \
    -d coldforge.xyz
```

**DNS challenge (for wildcard or servers without HTTP):**
```bash
sudo certbot certonly --manual \
    --preferred-challenges dns \
    -d mail.coldforge.xyz \
    -d coldforge.xyz
```

### Certificate Locations

After generation:
```
/etc/letsencrypt/live/mail.coldforge.xyz/
├── fullchain.pem  # Certificate + intermediate chain
├── privkey.pem    # Private key
├── cert.pem       # Certificate only
└── chain.pem      # Intermediate chain only
```

### Auto-Renewal

Certbot sets up automatic renewal. Verify:
```bash
sudo certbot renew --dry-run
```

Add post-renewal hook to restart services:
```bash
# /etc/letsencrypt/renewal-hooks/post/restart-cloistr.sh
#!/bin/bash
systemctl restart cloistr-email
# Or for Docker:
docker-compose restart backend
```

## Option 2: Kubernetes cert-manager

For Kubernetes deployments, use cert-manager for automatic certificate management.

### Install cert-manager

```bash
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.14.0/cert-manager.yaml
```

### Create ClusterIssuer

```yaml
# cert-manager-issuer.yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: admin@coldforge.xyz
    privateKeySecretRef:
      name: letsencrypt-prod-account-key
    solvers:
    - http01:
        ingress:
          class: nginx
---
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-staging
spec:
  acme:
    server: https://acme-staging-v02.api.letsencrypt.org/directory
    email: admin@coldforge.xyz
    privateKeySecretRef:
      name: letsencrypt-staging-account-key
    solvers:
    - http01:
        ingress:
          class: nginx
```

### Request Certificate

```yaml
# smtp-certificate.yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: cloistr-email-tls
  namespace: coldforge
spec:
  secretName: cloistr-email-tls
  issuerRef:
    name: letsencrypt-prod
    kind: ClusterIssuer
  commonName: mail.coldforge.xyz
  dnsNames:
  - mail.coldforge.xyz
  - coldforge.xyz
```

### Use in Deployment

```yaml
volumes:
- name: tls-certs
  secret:
    secretName: cloistr-email-tls

volumeMounts:
- name: tls-certs
  mountPath: /etc/certs
  readOnly: true
```

## Option 3: Self-Signed Certificates (Development Only)

**Do not use in production** - recipients will reject connections.

```bash
# Generate self-signed certificate
openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
    -keyout /etc/ssl/private/mail.key \
    -out /etc/ssl/certs/mail.crt \
    -subj "/CN=mail.coldforge.xyz"
```

## Configuring Cloistr Email

### Environment Variables

```bash
# SMTP Inbound TLS
SMTP_INBOUND_TLS_CERT=/etc/letsencrypt/live/mail.coldforge.xyz/fullchain.pem
SMTP_INBOUND_TLS_KEY=/etc/letsencrypt/live/mail.coldforge.xyz/privkey.pem

# Enable TLS (default: true when certs provided)
SMTP_INBOUND_TLS_ENABLED=true
```

### Docker Compose

```yaml
services:
  backend:
    volumes:
      - /etc/letsencrypt:/etc/letsencrypt:ro
    environment:
      - SMTP_INBOUND_TLS_CERT=/etc/letsencrypt/live/mail.coldforge.xyz/fullchain.pem
      - SMTP_INBOUND_TLS_KEY=/etc/letsencrypt/live/mail.coldforge.xyz/privkey.pem
```

### Kubernetes Secret

```bash
# Create TLS secret from cert files
kubectl create secret tls cloistr-email-tls \
    --cert=/etc/letsencrypt/live/mail.coldforge.xyz/fullchain.pem \
    --key=/etc/letsencrypt/live/mail.coldforge.xyz/privkey.pem \
    -n coldforge
```

## TLS Verification

### Test SMTP TLS

```bash
# Test STARTTLS on port 25
openssl s_client -connect mail.coldforge.xyz:25 -starttls smtp

# Test implicit TLS on port 465
openssl s_client -connect mail.coldforge.xyz:465

# Check certificate details
echo | openssl s_client -connect mail.coldforge.xyz:25 -starttls smtp 2>/dev/null | \
    openssl x509 -noout -dates -subject -issuer
```

### Test HTTPS TLS

```bash
# Check web certificate
curl -vI https://mail.coldforge.xyz 2>&1 | grep -A 6 "Server certificate"

# Test with SSL Labs
# https://www.ssllabs.com/ssltest/analyze.html?d=mail.coldforge.xyz
```

## Certificate Best Practices

### 1. Use Strong Key Size
- RSA: 2048-bit minimum, 4096-bit recommended
- ECDSA: P-256 or P-384 (faster, smaller)

### 2. Include All Required SANs
```bash
# Certificate should include:
# - mail.coldforge.xyz (MX hostname)
# - coldforge.xyz (domain)
# - smtp.coldforge.xyz (optional alias)
```

### 3. Monitor Expiration
```bash
# Check expiration date
openssl x509 -in /etc/letsencrypt/live/mail.coldforge.xyz/cert.pem -noout -enddate

# Set up monitoring alert (example for Prometheus)
# See alerting rules in k8s/prometheus-rules.yaml
```

### 4. Secure Private Keys
```bash
# Restrict permissions
chmod 600 /etc/letsencrypt/live/mail.coldforge.xyz/privkey.pem
chown root:root /etc/letsencrypt/live/mail.coldforge.xyz/privkey.pem
```

### 5. Use HSTS for Web
Add header to nginx/ingress:
```
Strict-Transport-Security: max-age=31536000; includeSubDomains
```

## SMTP TLS Modes

### STARTTLS (Port 25/587)
- Connection starts unencrypted
- `STARTTLS` command upgrades to TLS
- Standard for server-to-server (port 25)
- Standard for submission (port 587)

### Implicit TLS (Port 465)
- Connection is TLS from the start
- Also known as SMTPS
- Recommended for client submission

### Configuration

```bash
# Enable both modes
SMTP_INBOUND_ADDR=:25           # STARTTLS
SMTP_INBOUND_ADDR_TLS=:465      # Implicit TLS (if supported)
```

## Troubleshooting

### Certificate Not Trusted
```
error: certificate signed by unknown authority
```
- Ensure `fullchain.pem` includes intermediate certificates
- Don't use self-signed certs in production

### Certificate Hostname Mismatch
```
error: certificate is valid for X, not Y
```
- Ensure certificate includes all required hostnames in SAN
- Check `DNS_NAMES` in cert-manager Certificate resource

### Permission Denied
```
error: open /etc/letsencrypt/...: permission denied
```
- Run with proper permissions or as root
- In Docker, ensure volume is mounted correctly

### Certificate Expired
```
error: certificate has expired
```
- Check certbot renewal: `certbot renew`
- Verify cron job is running: `systemctl status certbot.timer`

## Related Documentation

- [DNS Setup](./DNS-SETUP.md) - Configure DNS records including TLSA
- [Production Deployment](./DEPLOYMENT.md) - Full deployment guide
- [Let's Encrypt Documentation](https://letsencrypt.org/docs/)
- [cert-manager Documentation](https://cert-manager.io/docs/)
