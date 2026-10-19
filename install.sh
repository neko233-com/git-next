#!/bin/sh
set -eu
prefix=${GIT_NEXT_PREFIX:-"${XDG_DATA_HOME:-$HOME/.local/share}/git-next"}
user_bin=${GIT_NEXT_BIN:-"$HOME/.local/bin"}
commit=${GIT_NEXT_COMMIT:-}
source_binary=${GIT_NEXT_SOURCE_BINARY:-}
case "${1:-}" in
  --replace-git) printf '%s\n' 'Replacement blocked: full Git compatibility has not passed. Install the isolated preview.' >&2; exit 1 ;;
  --uninstall)
    [ -f "$prefix/installed-command.sha256" ] || { printf '%s\n' 'No installation manifest.' >&2; exit 1; }
    expected=$(cut -d ' ' -f 1 "$prefix/installed-command.sha256")
    if [ -f "$user_bin/git-next" ]; then
      if command -v sha256sum >/dev/null; then actual=$(sha256sum "$user_bin/git-next" | cut -d ' ' -f 1); else actual=$(shasum -a 256 "$user_bin/git-next" | cut -d ' ' -f 1); fi
      [ "$actual" = "$expected" ] || { printf '%s\n' 'Command changed since installation; preserved.' >&2; exit 1; }
      rm "$user_bin/git-next"
    fi
    printf '%s\n' 'git-next preview command removed. Repositories, Git configuration and the original Git are preserved.'
    exit 0 ;;
  '') ;;
  *) printf '%s\n' 'Use install.sh [--replace-git|--uninstall]. Configuration: GIT_NEXT_PREFIX, GIT_NEXT_COMMIT, GIT_NEXT_SOURCE_BINARY.' >&2; exit 1 ;;
esac
case "$(uname -s):$(uname -m)" in Linux:x86_64|Linux:aarch64|Linux:arm64|Darwin:arm64) ;; *) printf '%s\n' 'Supported Unix targets: Linux x64/ARM64 and macOS ARM64.' >&2; exit 1 ;; esac
if [ -z "$source_binary" ]; then
  command -v cargo >/dev/null || { printf '%s\n' 'Install Rust stable and a native C compiler first: https://rustup.rs/' >&2; exit 1; }
  command -v curl >/dev/null || { printf '%s\n' 'curl is required to resolve the official commit.' >&2; exit 1; }
  if [ -z "$commit" ]; then
    commit=$(curl -q --fail --silent --show-error --proto '=https' --tlsv1.2 --max-time 30 -H 'User-Agent: git-next-installer' https://api.github.com/repos/neko233-com/git-next/commits/main | sed -n 's/^ *"sha": *"\([0-9a-f]*\)",*$/\1/p' | head -n 1)
  fi
  case "$commit" in *[!0-9a-f]*|'') printf '%s\n' 'Invalid pinned commit.' >&2; exit 1 ;; esac
  [ "${#commit}" = 40 ] || { printf '%s\n' 'A full official commit SHA is required.' >&2; exit 1; }
  export CARGO_NET_RETRY=1 CARGO_HTTP_TIMEOUT=30
  mkdir -p "$prefix"
  : > "$prefix/installer-empty-gitconfig"
  success=0
  for source in https://github.com/neko233-com/git-next.git https://ghfast.top/https://github.com/neko233-com/git-next.git https://ghproxy.net/https://github.com/neko233-com/git-next.git; do
    if GIT_CONFIG_GLOBAL="$prefix/installer-empty-gitconfig" GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_COUNT=0 GIT_TERMINAL_PROMPT=0 cargo install --config net.git-fetch-with-cli=false --git "$source" --rev "$commit" --locked --bin git-next --root "$prefix" --force; then success=1; break; fi
  done
  [ "$success" = 1 ] || { printf '%s\n' 'All source transports failed; existing Git was not modified.' >&2; exit 1; }
  source_binary="$prefix/bin/git-next"
fi
"$source_binary" doctor
mkdir -p "$user_bin" "$prefix/preview/bin"
if [ -f "$user_bin/git-next" ]; then cp -p "$user_bin/git-next" "$prefix/git-next.previous.$(date +%s)"; fi
cp "$source_binary" "$user_bin/git-next.new"
chmod 755 "$user_bin/git-next.new"
mv -f "$user_bin/git-next.new" "$user_bin/git-next"
cp "$source_binary" "$prefix/preview/bin/git"
chmod 755 "$prefix/preview/bin/git"
if command -v sha256sum >/dev/null; then sha256sum "$user_bin/git-next" > "$prefix/installed-command.sha256"; else shasum -a 256 "$user_bin/git-next" > "$prefix/installed-command.sha256"; fi
printf '%s\n' "Installed preview: $user_bin/git-next" 'Add its directory to PATH if needed. The git-named preview is outside PATH; existing Git is unchanged.'
