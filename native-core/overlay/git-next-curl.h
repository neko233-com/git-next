/* Copyright 2026 git-next contributors. SPDX-License-Identifier: GPL-2.0-only */
#ifndef GIT_NEXT_CURL_H
#define GIT_NEXT_CURL_H
#if defined(GIT_WINDOWS_NATIVE) && !defined(NO_CURL)
#include <curl/curl.h>

#define GIT_NEXT_CURL_FUNCTIONS(F) \
	F(CURLcode, global_init, (long)) \
	F(void, global_cleanup, (void)) \
	F(CURLsslset, global_sslset, (curl_sslbackend, const char *, const curl_ssl_backend ***)) \
	F(CURLcode, global_trace, (const char *)) \
	F(curl_version_info_data *, version_info, (CURLversion)) \
	F(CURL *, easy_init, (void)) \
	F(CURL *, easy_duphandle, (CURL *)) \
	F(void, easy_cleanup, (CURL *)) \
	F(CURLcode, easy_perform, (CURL *)) \
	F(CURLcode, easy_setopt, (CURL *, CURLoption, ...)) \
	F(CURLcode, easy_getinfo, (CURL *, CURLINFO, ...)) \
	F(char *, easy_escape, (CURL *, const char *, int)) \
	F(const char *, easy_strerror, (CURLcode)) \
	F(void, free, (void *)) \
	F(struct curl_slist *, slist_append, (struct curl_slist *, const char *)) \
	F(void, slist_free_all, (struct curl_slist *)) \
	F(CURLM *, multi_init, (void)) \
	F(CURLMcode, multi_add_handle, (CURLM *, CURL *)) \
	F(CURLMcode, multi_remove_handle, (CURLM *, CURL *)) \
	F(CURLMcode, multi_cleanup, (CURLM *)) \
	F(CURLMcode, multi_perform, (CURLM *, int *)) \
	F(CURLMcode, multi_fdset, (CURLM *, fd_set *, fd_set *, fd_set *, int *)) \
	F(CURLMcode, multi_timeout, (CURLM *, long *)) \
	F(CURLMsg *, multi_info_read, (CURLM *, int *)) \
	F(const char *, multi_strerror, (CURLMcode))

struct git_next_curl_api {
#define GIT_NEXT_CURL_DECLARE(result, name, arguments) result (*name) arguments;
	GIT_NEXT_CURL_FUNCTIONS(GIT_NEXT_CURL_DECLARE)
#undef GIT_NEXT_CURL_DECLARE
};
extern struct git_next_curl_api git_next_curl_api;
CURLcode git_next_curl_global_init(long flags);
CURLsslset git_next_curl_global_sslset(curl_sslbackend id, const char *name,
				     const curl_ssl_backend ***available);
CURL *git_next_curl_easy_init(void);

#ifndef GIT_NEXT_CURL_IMPLEMENTATION
#undef curl_easy_setopt
#undef curl_easy_getinfo
#define curl_global_init git_next_curl_global_init
#define curl_global_sslset git_next_curl_global_sslset
#define curl_easy_init git_next_curl_easy_init
#define curl_global_cleanup git_next_curl_api.global_cleanup
#define curl_global_trace git_next_curl_api.global_trace
#define curl_version_info git_next_curl_api.version_info
#define curl_easy_duphandle git_next_curl_api.easy_duphandle
#define curl_easy_cleanup git_next_curl_api.easy_cleanup
#define curl_easy_perform git_next_curl_api.easy_perform
#define curl_easy_setopt git_next_curl_api.easy_setopt
#define curl_easy_getinfo git_next_curl_api.easy_getinfo
#define curl_easy_escape git_next_curl_api.easy_escape
#define curl_easy_strerror git_next_curl_api.easy_strerror
#define curl_free git_next_curl_api.free
#define curl_slist_append git_next_curl_api.slist_append
#define curl_slist_free_all git_next_curl_api.slist_free_all
#define curl_multi_init git_next_curl_api.multi_init
#define curl_multi_add_handle git_next_curl_api.multi_add_handle
#define curl_multi_remove_handle git_next_curl_api.multi_remove_handle
#define curl_multi_cleanup git_next_curl_api.multi_cleanup
#define curl_multi_perform git_next_curl_api.multi_perform
#define curl_multi_fdset git_next_curl_api.multi_fdset
#define curl_multi_timeout git_next_curl_api.multi_timeout
#define curl_multi_info_read git_next_curl_api.multi_info_read
#define curl_multi_strerror git_next_curl_api.multi_strerror
#endif
#endif
#endif
