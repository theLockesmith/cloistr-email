# DNS Configuration for Cloistr Email

This guide covers all DNS records required for production email delivery.

## Required DNS Records

### 1. MX Record (Mail Exchange)

Routes incoming email to your mail server.

```
coldforge.xyz.    IN MX    10 mail.coldforge.xyz.
```

- **Priority 10**: Lower number = higher priority
- Point to your SMTP inbound server hostname

### 2. A/AAAA Records

Point your mail server hostname to its IP address.

```
mail.coldforge.xyz.    IN A       <YOUR_SERVER_IP>
mail.coldforge.xyz.    IN AAAA    <YOUR_IPV6_ADDRESS>  # Optional but recommended
```

### 3. SPF Record (Sender Policy Framework)

Specifies which servers can send email for your domain.

```
coldforge.xyz.    IN TXT    "v=spf1 mx a:mail.coldforge.xyz -all"
```

**Explanation:**
- `v=spf1` - SPF version
- `mx` - Allow servers listed in MX records
- `a:mail.coldforge.xyz` - Allow this specific hostname
- `-all` - Reject (hard fail) all other sources

**Softer option for testing:**
```
coldforge.xyz.    IN TXT    "v=spf1 mx ~all"
```
- `~all` - Soft fail (mark as suspicious but deliver)

### 4. DKIM Record (DomainKeys Identified Mail)

Cryptographic signature verification for outbound email.

**Generate keys first:**
```bash
./scripts/generate-dkim-keys.sh -d coldforge.xyz -s mail
```

**Add DNS record:**
```
mail._domainkey.coldforge.xyz.    IN TXT    "v=DKIM1; k=rsa; p=<PUBLIC_KEY_BASE64>"
```

**Key rotation:**
- Use dated selectors: `mail2024`, `mail2025`
- Add new key before removing old one
- Update `DKIM_SELECTOR` in production config

### 5. DMARC Record (Domain-based Message Authentication)

Policy for handling authentication failures.

```
_dmarc.coldforge.xyz.    IN TXT    "v=DMARC1; p=quarantine; rua=mailto:dmarc@coldforge.xyz; ruf=mailto:dmarc@coldforge.xyz; sp=quarantine; adkim=r; aspf=r"
```

**Explanation:**
- `v=DMARC1` - DMARC version
- `p=quarantine` - Policy for failures (none/quarantine/reject)
- `rua=mailto:...` - Aggregate report destination
- `ruf=mailto:...` - Forensic report destination
- `sp=quarantine` - Subdomain policy
- `adkim=r` - DKIM alignment (r=relaxed, s=strict)
- `aspf=r` - SPF alignment (r=relaxed, s=strict)

**Deployment stages:**
1. Start with `p=none` to monitor
2. Move to `p=quarantine` after verification
3. Optionally move to `p=reject` for strict enforcement

### 6. PTR Record (Reverse DNS)

Maps IP address back to hostname. **Required by many mail servers.**

Contact your hosting provider to set:
```
<IP_REVERSED>.in-addr.arpa.    IN PTR    mail.coldforge.xyz.
```

Example for IP 192.0.2.1:
```
1.2.0.192.in-addr.arpa.    IN PTR    mail.coldforge.xyz.
```

### 7. MTA-STS Record (Mail Transfer Agent Strict Transport Security)

Enforces TLS for incoming connections.

**DNS record:**
```
_mta-sts.coldforge.xyz.    IN TXT    "v=STSv1; id=20240101"
```

**Policy file** (serve at `https://mta-sts.coldforge.xyz/.well-known/mta-sts.txt`):
```
version: STSv1
mode: enforce
mx: mail.coldforge.xyz
max_age: 604800
```

### 8. TLSA Record (DANE - DNS-based Authentication)

Pins TLS certificate in DNS. Optional but adds security.

```
_25._tcp.mail.coldforge.xyz.    IN TLSA    3 1 1 <CERTIFICATE_HASH>
```

Generate hash:
```bash
openssl x509 -in /path/to/cert.pem -noout -pubkey | \
  openssl pkey -pubin -outform DER | \
  openssl sha256
```

## Complete DNS Zone Example

```zone
; coldforge.xyz DNS Zone for Email
$TTL 3600

; Mail server
mail                IN A        203.0.113.10
mail                IN AAAA     2001:db8::10

; MX record
@                   IN MX       10 mail.coldforge.xyz.

; SPF
@                   IN TXT      "v=spf1 mx -all"

; DKIM (replace with actual public key)
mail._domainkey     IN TXT      "v=DKIM1; k=rsa; p=MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA..."

; DMARC
_dmarc              IN TXT      "v=DMARC1; p=quarantine; rua=mailto:dmarc@coldforge.xyz"

; MTA-STS
_mta-sts            IN TXT      "v=STSv1; id=20240101"

; SMTP TLS Reporting
_smtp._tls          IN TXT      "v=TLSRPTv1; rua=mailto:tlsrpt@coldforge.xyz"
```

## Verification Commands

### Check MX Record
```bash
dig MX coldforge.xyz +short
# Expected: 10 mail.coldforge.xyz.
```

### Check SPF Record
```bash
dig TXT coldforge.xyz +short | grep spf
# Expected: "v=spf1 mx -all"
```

### Check DKIM Record
```bash
dig TXT mail._domainkey.coldforge.xyz +short
# Expected: "v=DKIM1; k=rsa; p=..."
```

### Check DMARC Record
```bash
dig TXT _dmarc.coldforge.xyz +short
# Expected: "v=DMARC1; p=quarantine; ..."
```

### Check PTR (Reverse DNS)
```bash
dig -x <YOUR_IP> +short
# Expected: mail.coldforge.xyz.
```

### Full Email Test
```bash
# Send test email and check headers
# Use: https://www.mail-tester.com/
# Or:  https://mxtoolbox.com/deliverability
```

## Common Issues

### SPF "Too Many DNS Lookups"
SPF has a 10 DNS lookup limit. If exceeded:
- Use `ip4:` and `ip6:` instead of `include:`
- Flatten SPF records with tools like spf-flattener

### DKIM Key Too Long for DNS
DNS TXT records have a 255-character limit per string.
- Split long keys: `"first part" "second part"`
- Most DNS providers handle this automatically

### DMARC Reports Not Arriving
- Ensure `rua` email address is valid and monitored
- Add external domain authorization if needed:
  ```
  coldforge.xyz._report._dmarc.external.com.    IN TXT    "v=DMARC1"
  ```

### PTR Record Mismatch
- PTR must match forward DNS exactly
- `mail.coldforge.xyz` must resolve to same IP that PTR points to
- Contact hosting provider - you usually can't set PTR yourself

## DNS Propagation

Changes can take 24-48 hours to propagate globally.

Monitor propagation:
```bash
# Check multiple DNS servers
dig @8.8.8.8 TXT _dmarc.coldforge.xyz
dig @1.1.1.1 TXT _dmarc.coldforge.xyz
dig @9.9.9.9 TXT _dmarc.coldforge.xyz
```

Or use: https://dnschecker.org/

## Security Recommendations

1. **Use `-all` (hard fail) for SPF** once verified working
2. **Use `p=reject` for DMARC** once confident in setup
3. **Rotate DKIM keys annually** with dated selectors
4. **Enable DNSSEC** if your registrar supports it
5. **Monitor DMARC reports** for unauthorized senders
6. **Set up MTA-STS** to enforce TLS

## Integration with Cloistr Email

After DNS is configured, set environment variables:

```bash
# .env.production
DKIM_DOMAIN=coldforge.xyz
DKIM_SELECTOR=mail
DKIM_PRIVATE_KEY="-----BEGIN RSA PRIVATE KEY-----
...
-----END RSA PRIVATE KEY-----"

SMTP_INBOUND_DOMAIN=coldforge.xyz
SMTP_INBOUND_DOMAINS=coldforge.xyz,mail.coldforge.xyz
```

See [Production Deployment Guide](./DEPLOYMENT.md) for full configuration.
