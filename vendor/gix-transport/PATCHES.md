# git-next HTTP transport patch

Source: gitoxide `gix-transport` 0.61.0 from the crates.io source package. Original MIT and Apache-2.0 licenses are retained.

The reqwest worker creates its reusable client after receiving transport configuration. It applies `http.sslCAInfo`, `http.sslVerify`, proxy/no-proxy, connect timeout and user-agent settings. Client instances are reused while these settings are unchanged. The upstream redirect and credential protections are retained, including HTTPS downgrade rejection and rejection of plain-HTTP credential transmission by default.

Validate using the authenticated TLS server in `tests/push_acceptance.py`: a generated private CA is trusted through repository-local `http.sslCAInfo`; certificate verification remains enabled. Fixture credentials and TLS private keys exist only in disposable mode-0600 files and never enter reports.

## TLS settings apply only to TLS URLs

`http.sslCAInfo` used to be applied to the client before the request scheme was known, and a bundle that yielded no certificate under rustls aborted every connection. curl ships `ca-bundle.trust.crt` on Windows, whose `TRUSTED CERTIFICATE` blocks rustls does not parse, so a perfectly valid Git configuration made *all* HTTP and HTTPS remotes fail - plain `http://` included.

Two changes:

- TLS configuration is only applied when the base URL is `https://`. A plain-text remote has no TLS state to configure.
- A configured bundle that parses to zero certificates no longer fails the connection; it is ignored and the default trust store stays in force, so certificates are still verified. `http.sslVerify` is unchanged and still disables verification for TLS URLs when set to `false`.

An unreadable file and a syntactically invalid bundle still fail, because those are configuration errors the user has to fix.

Validate by cloning an `http://` remote with a `ca-bundle.trust.crt` configured: before the change it reports `Configured TLS CA bundle has no certificates`, after it clones.
