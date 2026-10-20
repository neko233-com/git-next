# Interface languages

Native English and Simplified Chinese are integrated in the source-built primary core. The language commands are registered as Git builtins, including alias dispatch; settings, product resources/version JSON and 6226 upstream Chinese translations plus 37 product messages (6263 total) compile on Windows x64. [D04 is implementation-complete](DELIVERY_STATUS.md); no new-core language/runtime regression has run. The installed earlier preview does not contain these commands. Final packages and multilingual acceptance remain pending.

## Commands

```sh
git language get
git language get --json
git language set chinese
git language set english
git language set auto
git language list
git --language english status
```

Use `git-next` instead of `git` until the final takeover gates pass. `chinese`/`zh`/`zh-CN`/`zh-Hans` and `english`/`en`/`en-US` select built-in languages. `auto`/`system` returns to automatic selection. `get` prints a stable canonical `chinese` or `english` identifier. Its JSON response includes `schema_version`, resolved `language`, configured selection, source and supported languages. Set confirmation follows the newly saved language. Subcommand and option names remain stable English CLI identifiers.

## Selection and performance

Order: per-command `--language`, `GIT_NEXT_LANGUAGE`, saved preference, then system preference. In automatic mode standard locale environment overrides are consulted in order `LC_ALL`, `LC_MESSAGES`, `LANGUAGE`, `LANG`. `C`/`POSIX` selects English. Otherwise the client uses the native preferred UI language on Windows and preferred language list on macOS, directly through native APIs in the C core; the earlier Rust preview uses its pinned `sys-locale` dependency. Linux follows its locale environment. Chinese variants use the Simplified Chinese catalog; unsupported language preferences fall back to English.

Language selection is initialized once per process, without a child shell, network request, translation service or background service. The direct version fast path retains stable product/build identity and avoids settings/repository discovery. A fixed catalog translates message templates; substitutions are inserted once, so a path containing `{count}`, English status phrases or other template-like text is preserved verbatim. Default and overridden languages belong to the final paired startup/status performance matrix.

## Settings and compatibility report

The new additive user setting is `~/.config/git-next/settings.json`, or `$XDG_CONFIG_HOME/git-next/settings.json`. `GIT_NEXT_CONFIG_HOME` overrides the git-next configuration directory directly for isolated tests or managed installation. An explicit override must be absolute. Settings contain `schema_version: 1` and `language`, use UTF-8 without BOM and LF, preserve unknown fields, and publish under an exclusive sibling lock. Invalid, oversized or unknown-version settings reject without replacement. Repository-local files, Git configuration, credentials and SSH settings are not modified by language selection.

Rollback to the previous executable leaves this additive configuration file in place; older binaries ignore it. No Git object/ref/index/LFS layout changes or migrations occur. Uninstall retains language preference so a later reinstall can reuse it.

Human help, native init/clone/fetch/switch/merge/reset/LF/LFS notices, long status groups/hints/operation summaries, doctor headings and known native diagnostic messages use the catalog. Uncatalogued underlying-library diagnostic detail remains in its original language so the actual cause is retained. Product identity, command identifiers, user-supplied text, filenames, branch names, commit messages and rebase todo commands are not translated. Porcelain v1/v2, NUL output, object/config/ref data, patches, LFS protocol and existing JSON contracts remain byte-stable. The catalog is separate from protocol generation; no search-and-replace of final stdout is used.

New languages require a catalog, native-selection mapping, aliases/help and the same byte-preservation/failure tests; runtime machine protocols do not need a translation layer. The C core compiles a catalog generated from the pinned upstream `po/zh_CN.po` and the product extension messages; no online language pack is required.

## Batch acceptance

The consolidated matrix must exercise native automatic selection, English/Chinese aliases, persistence across processes, single-command/environment overrides, reset to auto, help/errors, common human messages, operation status, conflicts and filenames containing translation/template tokens. It must compare all machine formats between languages and verify no repository/config/login changes from language operations. Malformed/oversized settings, unknown schema/language, lock contention, read-only configuration and failed writes must preserve prior bytes. Supported platform compilation and actual runtime results remain separate evidence.

## Shell and Perl messages

Owned shell scripts and `Git::I18N` use the same compiled native catalog through the internal `i18n--gettext` builtin. The shell variable whitelist remains in the owned envsubst helper. Perl preserves input encoding and plural selection and resolves the owned core relative to its module, without searching for an installed Git. MSYS argument conversion is disabled only around translation calls so message templates remain literal. This bridge compiles; actual shell/Perl translation and byte-preservation acceptance belongs to D08.
