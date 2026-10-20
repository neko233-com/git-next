#!/bin/sh
set -eu
self=$0
case "$self" in */*) ;; *) self=$(command -v "$self");; esac
package=$(CDPATH= cd -- "${self%/*}" && pwd -P)
exec "$package/libexec/git-next/updater" Install --package "$package" "$@"
