# coldforge-email API Documentation

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
      "email": "user@coldforge.xyz"
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

### GET /emails

List emails for authenticated user.

**Query Parameters:**
- `limit` (default: 50) - Number of emails to return
- `offset` (default: 0) - Offset for pagination
- `status` (optional) - Filter by status (active, deleted, archived, spam)
- `folder` (optional) - Filter by folder (INBOX, SENT, DRAFTS, etc.)
- `direction` (optional) - Filter by direction (sent, received)

**Response (200 OK):**
```json
{
  "status": "ok",
  "data": {
    "emails": [
      {
        "id": "uuid",
        "from": "alice@coldforge.xyz",
        "to": "bob@example.com",
        "subject": "Hello Bob",
        "preview": "This is a preview of the email...",
        "is_encrypted": false,
        "created_at": "2026-01-17T10:00:00Z",
        "read": true
      }
    ],
    "total": 42,
    "limit": 50,
    "offset": 0
  }
}
```

### POST /emails

Send a new email.

**Request:**
```json
{
  "to": "bob@example.com",
  "subject": "Hello Bob",
  "body": "This is the email body",
  "encrypt": false,
  "attachments": [
    {
      "filename": "document.pdf",
      "blossom_sha256": "abc123...",
      "content_type": "application/pdf"
    }
  ]
}
```

**Response (201 Created):**
```json
{
  "status": "ok",
  "data": {
    "id": "uuid",
    "to": "bob@example.com",
    "subject": "Hello Bob",
    "created_at": "2026-01-17T10:00:00Z"
  }
}
```

**Response (400 Bad Request):**
```json
{
  "status": "error",
  "error": "invalid_recipient",
  "message": "Email address is not valid"
}
```

### GET /emails/{id}

Get a single email.

**Response (200 OK):**
```json
{
  "status": "ok",
  "data": {
    "id": "uuid",
    "from": "alice@coldforge.xyz",
    "to": "bob@example.com",
    "subject": "Hello Bob",
    "body": "This is the full email body",
    "is_encrypted": false,
    "sender_npub": "npub1alice...",
    "recipient_npub": "npub1bob...",
    "created_at": "2026-01-17T10:00:00Z",
    "read": true,
    "attachments": [
      {
        "id": "uuid",
        "filename": "document.pdf",
        "size_bytes": 102400,
        "blossom_url": "https://blossom.coldforge.xyz/abc123..."
      }
    ]
  }
}
```

### POST /emails/{id}/reply

Send a reply to an email.

**Request:**
```json
{
  "body": "This is the reply",
  "encrypt": false
}
```

**Response (201 Created):**
```json
{
  "status": "ok",
  "data": {
    "id": "uuid",
    "to": "alice@coldforge.xyz",
    "subject": "Re: Hello Bob",
    "created_at": "2026-01-17T10:05:00Z"
  }
}
```

### DELETE /emails/{id}

Delete an email.

**Response (200 OK):**
```json
{
  "status": "ok",
  "message": "Email deleted"
}
```

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

### POST /keys/import

Import a contact's Nostr key.

**Request:**
```json
{
  "email": "bob@example.com",
  "npub": "npub1bob..."
}
```

**Response (201 Created):**
```json
{
  "status": "ok",
  "data": {
    "id": "uuid",
    "email": "bob@example.com",
    "npub": "npub1bob...",
    "imported": true
  }
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
    "email": "user@coldforge.xyz",
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
  "service": "coldforge-email",
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
