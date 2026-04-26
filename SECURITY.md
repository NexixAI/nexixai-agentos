# Security Policy

AgentOS is a multi-tenant agent orchestration platform. The security surface is substantial; we welcome responsible disclosure.

## Reporting

Use GitHub's private security advisory feature:

1. Go to the repository's "Security" tab
2. Click "Report a vulnerability"
3. Fill in the details

Do NOT open a public issue for a security vulnerability.

## Scope

In-scope:

- Tenant isolation flaws (cross-tenant reads/writes/events)
- Authentication bypass (OIDC validation, API key, federation token)
- Privilege escalation across roles or scopes
- Header-trust vulnerabilities (caller-controlled identity headers being accepted as authoritative)
- SSRF via tool execution
- Symlink / path-traversal escapes from sandboxed contexts
- Prompt-injection paths that bypass governance enforcement

Out of scope:

- Vulnerabilities in third-party dependencies (report to upstream)
- Configuration mistakes in deployment by adopters (those are operational concerns)
- Performance issues that aren't security-relevant

## Response timeline

Acknowledgement within 5 business days. Coordinated disclosure timeline negotiated with the reporter.
