#!/bin/bash
# DKIM Key Generation Script for Cloistr Email
# Generates RSA key pair and outputs DNS TXT record

set -euo pipefail

# Configuration
DOMAIN="${DKIM_DOMAIN:-cloistr.xyz}"
SELECTOR="${DKIM_SELECTOR:-mail}"
KEY_SIZE="${DKIM_KEY_SIZE:-2048}"
OUTPUT_DIR="${DKIM_OUTPUT_DIR:-./dkim-keys}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

print_usage() {
    cat << EOF
Usage: $(basename "$0") [OPTIONS]

Generate DKIM keys for email authentication.

Options:
    -d, --domain DOMAIN     Domain to sign for (default: cloistr.xyz)
    -s, --selector SELECTOR DKIM selector (default: mail)
    -k, --key-size SIZE     RSA key size in bits (default: 2048)
    -o, --output DIR        Output directory (default: ./dkim-keys)
    -h, --help              Show this help message

Environment Variables:
    DKIM_DOMAIN             Same as --domain
    DKIM_SELECTOR           Same as --selector
    DKIM_KEY_SIZE           Same as --key-size
    DKIM_OUTPUT_DIR         Same as --output

Examples:
    $(basename "$0")
    $(basename "$0") -d example.com -s mail2024
    DKIM_DOMAIN=mysite.com $(basename "$0")
EOF
}

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -d|--domain)
            DOMAIN="$2"
            shift 2
            ;;
        -s|--selector)
            SELECTOR="$2"
            shift 2
            ;;
        -k|--key-size)
            KEY_SIZE="$2"
            shift 2
            ;;
        -o|--output)
            OUTPUT_DIR="$2"
            shift 2
            ;;
        -h|--help)
            print_usage
            exit 0
            ;;
        *)
            echo -e "${RED}Error: Unknown option $1${NC}"
            print_usage
            exit 1
            ;;
    esac
done

echo -e "${BLUE}=== DKIM Key Generation for Cloistr Email ===${NC}"
echo ""
echo -e "Domain:   ${GREEN}${DOMAIN}${NC}"
echo -e "Selector: ${GREEN}${SELECTOR}${NC}"
echo -e "Key Size: ${GREEN}${KEY_SIZE}${NC} bits"
echo -e "Output:   ${GREEN}${OUTPUT_DIR}${NC}"
echo ""

# Create output directory
mkdir -p "${OUTPUT_DIR}"

PRIVATE_KEY_FILE="${OUTPUT_DIR}/${SELECTOR}.${DOMAIN}.private.pem"
PUBLIC_KEY_FILE="${OUTPUT_DIR}/${SELECTOR}.${DOMAIN}.public.pem"
DNS_RECORD_FILE="${OUTPUT_DIR}/${SELECTOR}.${DOMAIN}.dns.txt"

# Check if keys already exist
if [[ -f "${PRIVATE_KEY_FILE}" ]]; then
    echo -e "${YELLOW}Warning: Private key already exists at ${PRIVATE_KEY_FILE}${NC}"
    read -p "Overwrite? (y/N) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "Aborted."
        exit 1
    fi
fi

# Generate RSA private key
echo -e "${BLUE}Generating ${KEY_SIZE}-bit RSA private key...${NC}"
openssl genrsa -out "${PRIVATE_KEY_FILE}" "${KEY_SIZE}" 2>/dev/null

# Extract public key
echo -e "${BLUE}Extracting public key...${NC}"
openssl rsa -in "${PRIVATE_KEY_FILE}" -pubout -out "${PUBLIC_KEY_FILE}" 2>/dev/null

# Generate DNS TXT record value
echo -e "${BLUE}Generating DNS TXT record...${NC}"

# Get public key without headers and newlines
PUBLIC_KEY_DATA=$(grep -v "^-" "${PUBLIC_KEY_FILE}" | tr -d '\n')

# Create DNS record
DNS_RECORD_NAME="${SELECTOR}._domainkey.${DOMAIN}"
DNS_RECORD_VALUE="v=DKIM1; k=rsa; p=${PUBLIC_KEY_DATA}"

# Save DNS record to file
cat > "${DNS_RECORD_FILE}" << EOF
# DKIM DNS TXT Record for ${DOMAIN}
# Selector: ${SELECTOR}
# Generated: $(date -u +"%Y-%m-%dT%H:%M:%SZ")
#
# Add this TXT record to your DNS:

Record Name: ${DNS_RECORD_NAME}
Record Type: TXT
Record Value:

${DNS_RECORD_VALUE}

# Verification command:
# dig TXT ${DNS_RECORD_NAME}
EOF

# Set restrictive permissions on private key
chmod 600 "${PRIVATE_KEY_FILE}"
chmod 644 "${PUBLIC_KEY_FILE}"
chmod 644 "${DNS_RECORD_FILE}"

echo ""
echo -e "${GREEN}=== Keys Generated Successfully ===${NC}"
echo ""
echo -e "Private Key: ${PRIVATE_KEY_FILE}"
echo -e "Public Key:  ${PUBLIC_KEY_FILE}"
echo -e "DNS Record:  ${DNS_RECORD_FILE}"
echo ""
echo -e "${YELLOW}=== DNS Configuration ===${NC}"
echo ""
echo -e "Add the following TXT record to your DNS:"
echo ""
echo -e "  ${GREEN}Name:${NC}  ${DNS_RECORD_NAME}"
echo -e "  ${GREEN}Type:${NC}  TXT"
echo -e "  ${GREEN}Value:${NC} v=DKIM1; k=rsa; p=..."
echo ""
echo -e "(Full value saved to ${DNS_RECORD_FILE})"
echo ""
echo -e "${YELLOW}=== Environment Variable ===${NC}"
echo ""
echo "Set this in your .env or deployment:"
echo ""
echo "DKIM_DOMAIN=${DOMAIN}"
echo "DKIM_SELECTOR=${SELECTOR}"
echo "DKIM_PRIVATE_KEY=\"\$(cat ${PRIVATE_KEY_FILE})\""
echo ""
echo -e "${YELLOW}=== Verify DNS Propagation ===${NC}"
echo ""
echo "After adding the DNS record, verify with:"
echo ""
echo "  dig TXT ${DNS_RECORD_NAME}"
echo "  # or"
echo "  nslookup -type=TXT ${DNS_RECORD_NAME}"
echo ""
echo -e "${BLUE}=== Security Notes ===${NC}"
echo ""
echo "1. Keep the private key secure - never commit it to git"
echo "2. Add ${OUTPUT_DIR}/ to your .gitignore"
echo "3. Use a secrets manager in production (Vault, K8s Secrets, etc.)"
echo "4. Rotate keys annually (create new selector: mail2025, mail2026, etc.)"
echo ""
