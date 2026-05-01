# Security Documentation

## Overview

This document describes the security architecture, threat model, and security best practices for the micro-one-api project.

## Security Architecture

### Authentication & Authorization

- **Authentication Method**: Bearer Token
- **Token Validation**: Through identity-service
- **Model Permissions**: Based on token model whitelist
- **Service-to-Service Authentication**: mTLS (configured via environment variables)

### Data Protection

- **Transport Encryption**: TLS 1.2+ (configurable)
- **Storage Encryption**: Database encryption (to be implemented)
- **Sensitive Data Sanitization**: Automatic log sanitization for API keys, tokens, and passwords

### Network Security

- **CORS**: Configurable allowed origins
- **Security Headers**: CSP, HSTS, X-Frame-Options, X-Content-Type-Options
- **Rate Limiting**: IP and token-based rate limiting
- **Request Size Limits**: Configurable maximum request body size

## Threat Model

### Mitigated Threats

- [x] SQL Injection (parameterized queries with GORM)
- [x] XSS (Content Security Policy)
- [x] CSRF (CSRF Token - to be implemented)
- [x] Rate Limiting Abuse (per-IP and per-token limits)
- [x] Large Request DoS (body size limits)
- [x] Log Data Leakage (automatic sanitization)

### Pending Mitigations

- [ ] Service-to-service authentication (mTLS setup required)
- [ ] Key management (Secrets Manager integration)
- [ ] Security logging audit (comprehensive event logging)
- [ ] IP access control (whitelist/blacklist implementation)

## Security Best Practices

### Development

- Use `gosec` for static analysis: `gosec ./...`
- Follow Go security coding guidelines
- Regular dependency updates: `go get -u ./...`
- Run security tests before committing

### Deployment

- Principle of least privilege
- Container security scanning
- Network segmentation
- Configuration management

### Operations

- Monitor security logs
- Regular security audits
- Incident response plan
- Security awareness training

## Configuration

### Environment Variables

See `.env.example` for all available security configuration options.

### Security Headers

The following security headers are automatically added to all responses:

- `X-Frame-Options: DENY`
- `X-Content-Type-Options: nosniff`
- `X-XSS-Protection: 1; mode=block`
- `Content-Security-Policy: default-src 'self'; ...`
- `Strict-Transport-Security: max-age=31536000; includeSubDomains; preload` (HTTPS only)
- `Referrer-Policy: strict-origin-when-cross-origin`
- `Permissions-Policy: geolocation=(), microphone=(), camera=(), ...`

### Rate Limiting

Rate limiting is implemented with the following default settings:

- Requests per second: 100
- Burst: 200
- Window: 1 minute

Configure via environment variables:

```bash
RATE_LIMIT_REQUESTS_PER_SECOND=100
RATE_LIMIT_BURST=200
```

### CORS

CORS is configurable via environment variable:

```bash
CORS_ALLOWED_ORIGINS=https://yourdomain.com,https://app.yourdomain.com
```

## Incident Response

### Event Classification

- **P0**: Data breach, system compromise
- **P1**: Denial of service, unauthorized access
- **P2**: Security misconfiguration
- **P3**: Potential security risk

### Response Process

1. Detection and confirmation
2. Containment and eradication
3. Recovery and verification
4. Post-incident analysis and improvement

## Security Contacts

- Security Team: security@example.com
- Emergency Contact: +86-xxx-xxxx-xxxx

## Security Testing

### Static Analysis

```bash
# Install and run gosec
go install github.com/securego/gosec/v2/cmd/gosec@latest
gosec ./...
```

### Dependency Scanning

```bash
# Run govulncheck
go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck ./...
```

### Secret Scanning

```bash
# Run gitleaks
gitleaks detect --source .
```

## Compliance

This project aims to comply with:

- OWASP Top 10 2025
- CWE/SANS Top 25
- GDPR (data protection)
- Industry security standards

## Change Log

### 2026-05-01

- Implemented security headers middleware
- Added rate limiting
- Added request body size limits
- Implemented input validation
- Added structured logging with sanitization
- Added CORS configuration
- Added timeout configurations
- Removed hardcoded credentials
- Added environment variable support
