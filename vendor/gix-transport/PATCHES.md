# git-next HTTP transport patch

Source: gitoxide `gix-transport` 0.61.0 from the crates.io source package. Original MIT and Apache-2.0 licenses are retained.

The reqwest worker creates its reusable client after receiving transport configuration. It applies `http.sslCAInfo`, `http.sslVerify`, proxy/no-proxy, connect timeout and user-agent settings. Client instances are reused while these settings are unchanged. The upstream redirect and credential protections are retained, including HTTPS downgrade rejection and rejection of plain-HTTP credential transmission by default.

Validate using the authenticated TLS server in `tests/push_acceptance.py`: a generated private CA is trusted through repository-local `http.sslCAInfo`; certificate verification remains enabled. Fixture credentials and TLS private keys exist only in disposable mode-0600 files and never enter reports.
