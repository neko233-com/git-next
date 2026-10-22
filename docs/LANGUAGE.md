# Interface languages

Every interface message of git-next comes from a locale document. A locale document is a JSON file in `locales/`; the build discovers the directory on its own and embeds every document it finds. Adding a language therefore means dropping in one file - there is no enumeration of supported languages anywhere in the code that could fall out of date, and no Rust change is required.

English is the source language. The message templates in the code are English, and a locale document only lists the messages it overrides. A message that a locale does not translate is shown in English rather than disappearing, so a partial translation degrades gracefully instead of breaking the interface.

See [delivery status](DELIVERY_STATUS.md) for final package and workstation acceptance.

## Commands

```sh
git language get
git language get --json
git language list
git language set zh-Hans
git language set en
git language set auto
git --language zh-Hans status
```

Use `git-next` before takeover, or `git` after installation activates its default entrypoint. `git language list` prints the shipped languages with their code, their name in that language and their name in English. `git language set` accepts a locale code (`zh-Hans`) or `auto` to follow the device. `git language get` prints the active locale code; its JSON response includes `schema_version`, the resolved `language`, its `name` and `english_name`, the configured selection, the source of the selection and the list of supported languages. Subcommand and option names remain stable English CLI identifiers.

The names that earlier releases accepted keep working: `chinese`, `zh`, `zh-CN`, `zh-Hans` and `中文` select Simplified Chinese, `english`, `en` and `en-US` select English, and `auto` or `system` returns to automatic selection. A configuration file that pins one of them keeps working unchanged.

## Selection and performance

Order: per-command `--language`, `GIT_NEXT_LANGUAGE`, saved preference, then the device preference.

In automatic mode the native preferred-UI-language API is the authority on Windows and on Apple platforms, because it reports the language the user actually chose for the system. The locale environment variables `LC_ALL`, `LC_MESSAGES`, `LANGUAGE` and `LANG` are consulted where no native API exists. A locale that states no preference - the POSIX `C` and `POSIX` locales - and a locale that names a language no document ships for both fall back to English, because guessing a related language would show messages the user cannot read.

The device locale is matched against the shipped documents from the most specific form to the least specific one: the full code, then the code with the region resolved into a writing system, then the code without its region, then the bare language. A device that reports `zh-CN` therefore finds `zh-Hans`, a device that reports `zh-TW` finds `zh-Hant` when that document is shipped, and a device that reports `pt-BR` finds `pt` when only a regional document is missing.

Language selection is initialized once per process, without a child shell, network request, translation service or background service. The direct version fast path retains stable product/build identity and avoids settings/repository discovery. A fixed catalog translates message templates; substitutions are inserted once, so a path containing `{count}`, English status phrases or other template-like text is preserved verbatim. Default and overridden languages belong to the final paired startup/status performance matrix.

## Adding a language

Create `locales/<code>.json`:

```json
{
  "code": "pt-BR",
  "name": "Português do Brasil",
  "english_name": "Brazilian Portuguese",
  "commands": {
    "status": "Mostrar o estado da área de trabalho"
  },
  "messages": {
    "On branch ": "No branch ",
    "Changes to be committed:\n": "Mudanças a serem confirmadas:\n"
  }
}
```

- `code` is the locale code. The file name is the identity of the locale, so a document cannot claim a code it was not published under.
- `name` is the name of the language in that language, and `english_name` is its name in English. The name is shown in the language list and in the confirmation of a language change.
- `messages` maps the English source message to its translation. Both the message and the translation may contain `{name}` placeholders, which are substituted once.
- `commands` maps a command name to its summary, which replaces the summary in the help output.

Rebuilding is the whole procedure: the build picks the file up, and the language appears in `git language list` and in the automatic device match. No registration list, no help text, no alias table and no test fixture has to be edited.

Only the messages that a locale overrides are listed, so a partial translation is a valid document. The English source message is the key, which means a message that is reworded in the code is simply not translated until the document catches up - the interface then shows the new English text.

## Settings and compatibility report

The new additive user setting is `~/.config/git-next/settings.json`, or `$XDG_CONFIG_HOME/git-next/settings.json`. `GIT_NEXT_CONFIG_HOME` overrides the git-next configuration directory directly for isolated tests or managed installation. An explicit override must be absolute. Settings contain `schema_version: 1` and `language`, use UTF-8 without BOM and LF, preserve unknown fields, and publish under an exclusive sibling lock. Invalid, oversized or unknown-version settings reject without replacement. Repository-local files, Git configuration, credentials and SSH settings are not modified by language selection.

Rollback to the previous executable leaves this additive configuration file in place; older binaries ignore it. No Git object/ref/index/LFS layout changes or migrations occur. Uninstall retains language preference so a later reinstall can reuse it.

Human help, native init/clone/fetch/switch/merge/reset/LF/LFS notices, long status groups/hints/operation summaries, doctor headings and known native diagnostic messages use the catalog. Uncatalogued underlying-library diagnostic detail remains in its original language so the actual cause is retained. Product identity, command identifiers, user-supplied text, filenames, branch names, commit messages and rebase todo commands are not translated. Porcelain v1/v2, NUL output, object/config/ref data, patches, LFS protocol and existing JSON contracts remain byte-stable. The catalog is separate from protocol generation; no search-and-replace of final stdout is used.

The native core compiles a catalog generated from the pinned upstream `po/zh_CN.po` and the product extension messages; no online language pack is required. The interface language of the native core and the language of the Rust interface are selected independently, because the native core has its own message set.

## Batch acceptance

The consolidated matrix must exercise native automatic selection, the legacy aliases, persistence across processes, single-command/environment overrides, reset to auto, help/errors, common human messages, operation status, conflicts and filenames containing translation/template tokens. It must compare all machine formats between languages and verify no repository/config/login changes from language operations. Malformed/oversized settings, unknown schema/language, lock contention, read-only configuration and failed writes must preserve prior bytes. Supported platform compilation and actual runtime results remain separate evidence.

A new locale document is accepted on the strength of the build and the interface: the messages of a new language are not machine-verified against a reference, because the reference for a translation is the language itself.

## Shell and Perl messages

Owned shell scripts and `Git::I18N` use the same compiled native catalog through the internal `i18n--gettext` builtin. The shell variable whitelist remains in the owned envsubst helper. Perl preserves input encoding and plural selection and resolves the owned core relative to its module, without searching for an installed Git. MSYS argument conversion is disabled only around translation calls so message templates remain literal. This bridge compiles; actual shell/Perl translation and byte-preservation acceptance belongs to D08.
