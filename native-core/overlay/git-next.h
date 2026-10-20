/* Copyright 2026 git-next contributors. SPDX-License-Identifier: GPL-2.0-only */
#ifndef GIT_NEXT_H
#define GIT_NEXT_H

extern int git_next_lf_defaults;
void git_next_prepare(const char **argv);
void git_next_setup_gettext(void);
const char *git_next_gettext(const char *message);
const char *git_next_ngettext(const char *singular, const char *plural,
                             unsigned long count);
int git_next_gettext_width(const char *message);
const char *git_next_preferred_languages(void);
void git_next_language_override(const char *value);
int git_next_command(int argc, const char **argv);
int git_next_version_json(void);
char *git_next_migration_config(void);
char *git_next_machine_path(const char *suffix);
struct strvec;
int git_next_prepare_interpreter(struct strvec *args, struct strvec *env);
struct repository;
void git_next_configure_new_repo(struct repository *repo);
int git_next_eol_command(int argc, const char **argv);
int cmd_git_next_language(int argc, const char **argv, const char *prefix,
                          struct repository *repo);
int cmd_git_next_gettext(int argc, const char **argv, const char *prefix,
                         struct repository *repo);
int cmd_git_next_eol(int argc, const char **argv, const char *prefix,
                     struct repository *repo);
int cmd_git_next_report(int argc, const char **argv, const char *prefix,
                        struct repository *repo);
int cmd_git_next_update(int argc, const char **argv, const char *prefix,
                        struct repository *repo);
#include "git-next-runtime.h"

#endif
