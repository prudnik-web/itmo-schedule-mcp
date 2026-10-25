# Security and privacy

## Trust boundaries

Default calendar sync runs on the user's machine. It sends ITMO credentials to
ITMO and uses Google OAuth to access the user's Google Calendar. Credentials and
refresh tokens are stored in private application state outside the repository.
Unix permissions and Windows access controls restrict other ordinary users;
administrators and software running as the account owner can access these secrets.
The storage is access-controlled, not a promise of encryption against the host owner.

The optional MCP source sends ITMO credentials to the selected MCP host. That
host's administrator can inspect them. HTTPS protects transport but does not
hide credentials from the recipient. Google tokens stay with the calendar client;
the read-only MCP exposes no Calendar operations. Caller credentials key separate
in-memory sessions. Shared host resource limits may affect availability.

Primary-calendar deletion is opt-in. Matches require exact times and strong
subject/link evidence; recurring events and events with guests are excluded.
The destination is verified and the original event backed up before deletion.
A missing schedule entry alone never causes deletion.

## Reporting

When the GitHub repository is available, use **Security → Report a vulnerability**
if private reporting is enabled. If it is unavailable, open an issue requesting a
private contact without including exploit details or account data. Do not post
passwords, bearer/refresh tokens, completed configuration, raw schedule responses,
or screenshots containing these values.

If a credential was exposed, revoke/replace it with its provider before sharing
sanitized diagnostics. Removing a secret from the latest commit does not remove
it from Git history or prior downloads.

## Maintainer checks

Only synthetic fixtures belong in source. Scan tracked files and history before
publication, keep OAuth clients and account state out of release allowlists, and
retain the Caddy custom-header redaction rules when hosting the MCP service.
Do not log request bodies or credentials to debug authentication.

The project uses an undocumented ITMO API, and interactive 2FA/CAPTCHA is not
supported. No guarantees are made about vulnerabilities unknown to the maintainers.
