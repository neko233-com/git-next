# Removed Python CLI suites

The 30 retired CLI scripts have been removed. Their source and exact historical
reports remain in Git history. There is no Python wrapper or executable gate.

Use [Rust acceptance](../../docs/GIT_NEXT_ACCEPTANCE.md) for L0/full/network.
Retirement does not claim that every historical assertion has been migrated.
The current scope and remaining coverage are recorded there; old passing reports
remain historical evidence for their exact bytes.

Reusable HTTP, HTTPS/LFS and SSH services now live in `tools/devtool/fixture_*.go`.
Native package, credential, migration and benchmark commands use the Go tool.
Native package, GUI, platform API and installer lifecycle acceptance remains
separate from Rust CLI conformance; necessary platform scripts are retained.
