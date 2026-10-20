/* Copyright 2026 git-next contributors. SPDX-License-Identifier: GPL-2.0-only */
#define USE_THE_REPOSITORY_VARIABLE
#include "git-compat-util.h"
#include "git-next.h"
#include "config.h"
#include "convert.h"
#include "dir.h"
#include "environment.h"
#include "gettext.h"
#include "parse-options.h"
#include "path.h"
#include "repository.h"
#include "setup.h"
#include "strbuf.h"
#include "yyjson.h"

static const char *policy_names[] = { ".gitattributes", ".editorconfig" };
static const char *policy_contents[] = {
	"# git-next: shared text normalization; binary detection and later project rules remain authoritative.\n"
	"* text=auto eol=lf\n",
	"# git-next: change line endings without changing indentation, encoding or whitespace rules.\n"
	"[*]\nend_of_line = lf\n"
};

static void validate_policy(const char *path)
{
	struct stat st;
	if (lstat(path, &st) < 0) {
		if (errno == ENOENT)
			return;
		die_errno(_("could not inspect line-ending policy"));
	}
	if (!S_ISREG(st.st_mode))
		die(_("existing line-ending policy must be a regular file"));
}

static unsigned int create_policies(struct repository *repo)
{
	const char *root = repo_get_work_tree(repo);
	char *paths[2];
	unsigned int created = 0;
	size_t i;
	if (!root)
		return 0;
	for (i = 0; i < ARRAY_SIZE(policy_names); i++) {
		paths[i] = xstrfmt("%s/%s", root, policy_names[i]);
		validate_policy(paths[i]);
	}
	for (i = 0; i < ARRAY_SIZE(policy_names); i++) {
		int fd = open(paths[i], O_WRONLY | O_CREAT | O_EXCL, 0666);
		if (fd < 0) {
			if (errno != EEXIST)
				die_errno(_("could not create line-ending policy"));
			validate_policy(paths[i]);
		} else {
			size_t length = strlen(policy_contents[i]);
			int failed = write_in_full(fd, policy_contents[i], length) != length;
			if (close(fd))
				failed = 1;
			if (failed)
				die_errno(_("could not save line-ending policy"));
			created |= 1U << i;
		}
		free(paths[i]);
	}
	return created;
}

static void configure_lf(struct repository *repo)
{
	repo_config_set(repo, "core.autocrlf", "input");
	repo_config_set(repo, "core.eol", "lf");
	/* Command-line overrides retain their normal priority over local defaults. */
	repo_config_clear(repo);
	repo_config(repo, git_default_core_config, NULL);
}

void git_next_configure_new_repo(struct repository *repo)
{
	configure_lf(repo);
}

static void refuse_active_operation(struct repository *repo)
{
	static const char *states[] = { "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD",
		"rebase-merge", "rebase-apply", "BISECT_LOG" };
	size_t i;
	for (i = 0; i < ARRAY_SIZE(states); i++) {
		char *path = repo_git_path(repo, "%s", states[i]);
		int present = file_exists(path);
		free(path);
		if (present)
			die(_("finish the active repository operation before changing LF policy"));
	}
}

static const char *config_value(struct repository *repo, const char *key)
{
	const char *value;
	if (repo_config_get_value(repo, key, &value))
		return NULL;
	return value ? value : "true";
}

int git_next_eol_command(int argc, const char **argv)
{
	const char *usage[] = { "git eol [--apply] [--json]", NULL };
	int apply = 0, json = 0;
	struct option options[] = {
		OPT_BOOL(0, "apply", &apply, N_("set LF configuration and create missing policy files")),
		OPT_BOOL(0, "json", &json, N_("print line-ending policy as JSON")),
		OPT_END()
	};
	struct repository *repo;
	const char *root, *autocrlf, *eol;
	yyjson_mut_doc *doc;
	yyjson_mut_val *report, *created_paths;
	unsigned int created = 0;
	char *text;
	size_t i, length;
	argc = parse_options(argc, argv, NULL, options, usage, 0);
	if (argc)
		usage_with_options(usage, options);
	setup_git_directory();
	repo = the_repository;
	root = repo_get_work_tree(repo);
	if (!root || is_bare_repository())
		die(_("line-ending policy requires a working tree"));
	if (apply) {
		refuse_active_operation(repo);
		for (i = 0; i < ARRAY_SIZE(policy_names); i++) {
			char *path = xstrfmt("%s/%s", root, policy_names[i]);
			validate_policy(path);
			free(path);
		}
		configure_lf(repo);
		created = create_policies(repo);
		repo_config_clear(repo);
	}
	autocrlf = config_value(repo, "core.autocrlf");
	eol = config_value(repo, "core.eol");
	if (!json) {
		printf(_("Line endings (target LF): core.autocrlf=%s, core.eol=%s\n"),
		       autocrlf ? autocrlf : "default", eol ? eol : "default");
		puts(_("Existing project rules preserved; no files staged or renormalized."));
		return 0;
	}
	doc = yyjson_mut_doc_new(NULL);
	if (!doc)
		die(_("out of memory writing line-ending policy report"));
	report = yyjson_mut_obj(doc);
	created_paths = yyjson_mut_arr(doc);
	yyjson_mut_doc_set_root(doc, report);
	yyjson_mut_obj_add_str(doc, report, "target_policy", "LF");
	if (autocrlf)
		yyjson_mut_obj_add_str(doc, report, "core_autocrlf", autocrlf);
	else
		yyjson_mut_obj_add_null(doc, report, "core_autocrlf");
	if (eol)
		yyjson_mut_obj_add_str(doc, report, "core_eol", eol);
	else
		yyjson_mut_obj_add_null(doc, report, "core_eol");
	for (i = 0; i < ARRAY_SIZE(policy_names); i++) {
		char *path = xstrfmt("%s/%s", root, policy_names[i]);
		yyjson_mut_obj_add_bool(doc, report,
			i ? "editorconfig_present" : "attributes_present", file_exists(path));
		if (created & (1U << i))
			yyjson_mut_arr_add_str(doc, created_paths, policy_names[i]);
		free(path);
	}
	yyjson_mut_obj_add_val(doc, report, "created", created_paths);
	yyjson_mut_obj_add_bool(doc, report, "existing_rules_preserved", 1);
	yyjson_mut_obj_add_bool(doc, report, "index_renormalized", 0);
	yyjson_mut_obj_add_str(doc, report, "migration",
		"Review tracked CRLF files, then use add --renormalize in a dedicated normalization change.");
	text = yyjson_mut_write(doc, YYJSON_WRITE_NEWLINE_AT_END, &length);
	if (!text)
		die(_("out of memory writing line-ending policy report"));
	fwrite(text, 1, length, stdout);
	free(text);
	yyjson_mut_doc_free(doc);
	return 0;
}
