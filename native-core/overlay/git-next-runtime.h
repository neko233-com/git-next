/* Copyright 2026 git-next contributors. SPDX-License-Identifier: GPL-2.0-only */
#ifndef GIT_NEXT_RUNTIME_H
#define GIT_NEXT_RUNTIME_H

#ifdef GIT_WINDOWS_NATIVE
#if defined(_M_ARM64) || defined(__aarch64__)
#define GIT_NEXT_RUNTIME_NATIVE_PREFIX "runtime/clangarm64"
#else
#define GIT_NEXT_RUNTIME_NATIVE_PREFIX "runtime/mingw64"
#endif
#else
#define GIT_NEXT_RUNTIME_NATIVE_PREFIX "runtime/usr"
#endif
#define GIT_NEXT_RUNTIME_NATIVE_BIN GIT_NEXT_RUNTIME_NATIVE_PREFIX "/bin"

/* Retain the native tools' relative layout beneath the owned package root. */
#undef GIT_EXEC_PATH
#undef GIT_LOCALE_PATH
#undef GIT_MAN_PATH
#undef GIT_INFO_PATH
#undef GIT_HTML_PATH
#undef DEFAULT_GIT_TEMPLATE_DIR
#undef ETC_GITCONFIG
#undef ETC_GITATTRIBUTES
#define GIT_EXEC_PATH GIT_NEXT_RUNTIME_NATIVE_PREFIX "/libexec/git-core"
#define GIT_LOCALE_PATH GIT_NEXT_RUNTIME_NATIVE_PREFIX "/share/locale"
#define GIT_MAN_PATH GIT_NEXT_RUNTIME_NATIVE_PREFIX "/share/man"
#define GIT_INFO_PATH GIT_NEXT_RUNTIME_NATIVE_PREFIX "/share/info"
#define GIT_HTML_PATH GIT_NEXT_RUNTIME_NATIVE_PREFIX "/share/doc/git-doc"
#define DEFAULT_GIT_TEMPLATE_DIR GIT_NEXT_RUNTIME_NATIVE_PREFIX "/share/git-core/templates"
#define ETC_GITCONFIG "runtime/etc/gitconfig"
#define ETC_GITATTRIBUTES "runtime/etc/gitattributes"
#if defined(GIT_WINDOWS_NATIVE) && !defined(GIT_SSL_CA_INFO)
#define GIT_SSL_CA_INFO GIT_NEXT_RUNTIME_NATIVE_PREFIX "/etc/ssl/certs/ca-bundle.crt"
#endif

#endif
