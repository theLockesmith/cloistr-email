# Cloistr Email API Documentation

## Base URL

```
http://localhost:8080/api/v1
```

## Authentication

All endpoints except `/auth/nip46/*` require a valid session token.

Pass token in the `Authorization` header:

```
Authorization: Bearer <session-token>
```

## Response Format

All responses are JSON.

### Success Response

```json
{
  "status": "ok",
  "data": { ... }
}
```

### Error Response

```json
{
  "status": "error",
  "error": "error_code",
  "message": "Human-readable error message"
}
```

## Authentication Endpoints

### POST /auth/nip46/challenge

Initiate NIP-46 authentication.

**Request:**
```json
{}
```

**Response (200 OK):**
```json
{
  "status": "ok",
  "data": {
    "challenge_id": "uuid",
    "challenge": "challenge-string",
    "expires_at": "2026-01-17T10:30:00Z"
  }
}
```

**Flow:**
1. Client calls this endpoint
2. Receives challenge ID and challenge content
3. Sends challenge to nsecbunker (user's signer) for signing
4. User approves signing request
5. Client receives signed event from nsecbunker
6. Client calls `/auth/nip46/verify` with the signature

### POST /auth/nip46/verify

Verify NIP-46 signature and create session.

**Request:**
```json
{
  "challenge_id": "uuid",
  "pubkey": "npub1...",
  "signature": "signed-event-json"
}
```

**Response (200 OK):**
```json
{
  "status": "ok",
  "data": {
    "session_id": "uuid",
    "token": "session-token",
    "user": {
      "npub": "npub1...",
      "email": "user@cloistr.xyz"
    },
    "expires_at": "2026-01-18T10:30:00Z"
  }
}
```

**Response (401 Unauthorized):**
```json
{
  "status": "error",
  "error": "invalid_signature",
  "message": "Signature verification failed"
}
```

### POST /auth/logout

Logout and invalidate session.

**Request:**
```json
{}
```

**Response (200 OK):**
```json
{
  "status": "ok",
  "message": "Logged out"
}
```

## Email Endpoints

Mail is served by the v2 API under `/api/v2/email` (list, read, send, delete,
archive, star, labels, move, bulk, attachments). A reply is a send that sets
`in_reply_to`. The v1 `/api/v1/emails` routes were stubs that reported success
without doing anything and have been removed; they now answer 404.

## Key Discovery Endpoints

### GET /keys/discover

Discover recipient's Nostr public key for encryption.

**Query Parameters:**
- `email` (required) - Email address to look up

**Response (200 OK):**
```json
{
  "status": "ok",
  "data": {
    "email": "bob@example.com",
    "npub": "npub1bob...",
    "method": "nip05",
    "verified": true
  }
}
```

**Response (404 Not Found):**
```json
{
  "status": "error",
  "error": "key_not_found",
  "message": "No Nostr key found for this email"
}
```

### GET /keys/mine

Get authenticated user's key information.

**Response (200 OK):**
```json
{
  "status": "ok",
  "data": {
    "npub": "npub1user...",
    "email": "user@cloistr.xyz",
    "public_key": "...",
    "encryption_method": "nip44"
  }
}
```

## Contact Endpoints

### GET /contacts

List contacts for authenticated user.

**Query Parameters:**
- `limit` (default: 50)
- `offset` (default: 0)
- `search` (optional) - Search by name or email

**Response (200 OK):**
```json
{
  "status": "ok",
  "data": {
    "contacts": [
      {
        "id": "uuid",
        "email": "bob@example.com",
        "name": "Bob Smith",
        "npub": "npub1bob...",
        "organization": "Example Corp",
        "always_encrypt": false,
        "created_at": "2026-01-10T10:00:00Z"
      }
    ],
    "total": 5,
    "limit": 50,
    "offset": 0
  }
}
```

### POST /contacts

Add a new contact.

**Request:**
```json
{
  "email": "bob@example.com",
  "name": "Bob Smith",
  "npub": "npub1bob...",
  "organization": "Example Corp",
  "phone": "+1234567890",
  "always_encrypt": false
}
```

**Response (201 Created):**
```json
{
  "status": "ok",
  "data": {
    "id": "uuid",
    "email": "bob@example.com",
    "name": "Bob Smith",
    "created_at": "2026-01-17T10:00:00Z"
  }
}
```

### GET /contacts/{id}

Get a single contact.

**Response (200 OK):**
```json
{
  "status": "ok",
  "data": {
    "id": "uuid",
    "email": "bob@example.com",
    "name": "Bob Smith",
    "npub": "npub1bob...",
    "organization": "Example Corp",
    "phone": "+1234567890",
    "notes": "Contact notes here",
    "always_encrypt": false,
    "blocked": false,
    "created_at": "2026-01-10T10:00:00Z",
    "updated_at": "2026-01-17T10:00:00Z"
  }
}
```

### DELETE /contacts/{id}

Delete a contact.

**Response (200 OK):**
```json
{
  "status": "ok",
  "message": "Contact deleted"
}
```

## Health Endpoints

### GET /health

Service health check.

**Response (200 OK):**
```json
{
  "status": "ok",
  "service": "cloistr-email",
  "timestamp": "2026-01-17T10:00:00Z"
}
```

### GET /ready

Readiness check (includes dependency checks).

**Response (200 OK):**
```json
{
  "status": "ready"
}
```

**Response (503 Service Unavailable):**
```json
{
  "status": "not_ready",
  "reason": "database"
}
```

## Error Codes

| Code | HTTP | Description |
|------|------|-------------|
| `invalid_request` | 400 | Request body is invalid |
| `invalid_token` | 401 | Session token is invalid or expired |
| `unauthorized` | 403 | User is not authorized for this resource |
| `not_found` | 404 | Resource not found |
| `invalid_signature` | 401 | NIP-46 signature verification failed |
| `key_not_found` | 404 | Nostr key not found for recipient |
| `encryption_failed` | 500 | Email encryption failed |
| `decryption_failed` | 500 | Email decryption failed |
| `stalwart_error` | 500 | Stalwart mail server error |
| `database_error` | 500 | Database error |
| `internal_error` | 500 | Internal server error |

## Rate Limiting

Rate limits may be applied per user:

- Authentication endpoints: 5 requests per minute
- Email sending: 10 emails per minute
- Other endpoints: 100 requests per minute

When rate limited, the response will be:

```json
{
  "status": "error",
  "error": "rate_limited",
  "message": "Too many requests",
  "retry_after": 60
}
```

HTTP status: `429 Too Many Requests`
