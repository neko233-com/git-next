// Copyright 2026 git-next contributors. MIT license.
//go:build linux || darwin

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const repository = "neko233-com/git-next"

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var commitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z.-]+))?$`)
var mirrorPrefixes = []string{"https://ghfast.top/", "https://ghproxy.net/"}

type updateEntry struct {
	Asset string `json:"asset"`
	SHA   string `json:"sha256"`
	Size  int64  `json:"size"`
	Kind  string `json:"install_kind"`
}

type updateManifest struct {
	Schema    int                    `json:"schema_version"`
	Product   string                 `json:"product"`
	Version   string                 `json:"version"`
	Commit    string                 `json:"source_commit"`
	Parity    bool                   `json:"complete_git_parity"`
	Platforms map[string]updateEntry `json:"platforms"`
}

type releaseAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

type githubRelease struct {
	Tag        string         `json:"tag_name"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	Assets     []releaseAsset `json:"assets"`
}

type updatePlan struct {
	Version, Commit, URL, ManifestSHA, Trust string
	Entry                                    updateEntry
	Newer                                    bool
}

func officialURL(version, name string) string {
	return "https://github.com/" + repository + "/releases/download/v" + version + "/" + name
}

func publicURL(raw string, official bool) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Hostname() == "" || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	if !official {
		return true
	}
	switch strings.ToLower(u.Hostname()) {
	case "api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return true
	}
	return false
}

func httpClient(official bool, timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 8 || !publicURL(req.URL.String(), official) {
			return fault("metadata-unavailable")
		}
		return nil
	}}
}

func request(ctx context.Context, method, raw string, official bool, timeout time.Duration) (*http.Response, error) {
	if !publicURL(raw, official) {
		return nil, fault("metadata-unavailable")
	}
	req, err := http.NewRequestWithContext(ctx, method, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "git-next-updater/1")
	req.Header.Set("Accept-Encoding", "identity")
	return httpClient(official, timeout).Do(req)
}

func metadata(raw string, limit int64) ([]byte, error) {
	response, err := request(context.Background(), "GET", raw, true, 30*time.Second)
	if err != nil {
		return nil, fault("metadata-unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > limit {
		return nil, fault("metadata-unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, fault("metadata-unavailable")
	}
	return data, nil
}

func versionParts(value string) ([3]uint64, string, error) {
	var parts [3]uint64
	match := versionPattern.FindStringSubmatch(value)
	if match == nil {
		return parts, "", fault("invalid-package")
	}
	for index := range parts {
		number, err := strconv.ParseUint(match[index+1], 10, 32)
		if err != nil {
			return parts, "", fault("invalid-package")
		}
		parts[index] = number
	}
	return parts, match[4], nil
}

func newerVersion(candidate, current string) (bool, error) {
	next, preview, err := versionParts(candidate)
	if err != nil || preview != "" {
		return false, fault("metadata-unavailable")
	}
	old, prior, err := versionParts(current)
	if err != nil {
		return false, err
	}
	for index := range next {
		if next[index] != old[index] {
			return next[index] > old[index], nil
		}
	}
	return prior != "", nil
}

func singleAsset(release githubRelease, name string) (releaseAsset, error) {
	var found []releaseAsset
	for _, asset := range release.Assets {
		if asset.Name == name {
			found = append(found, asset)
		}
	}
	if len(found) != 1 {
		return releaseAsset{}, fault("metadata-unavailable")
	}
	return found[0], nil
}

func officialPlan(installed packageManifest) (updatePlan, error) {
	var release githubRelease
	var manifest updateManifest
	api, apiErr := metadata("https://api.github.com/repos/"+repository+"/releases/latest", 2<<20)
	manifestURL, trust := "https://github.com/"+repository+"/releases/latest/download/git-next-update.json", "official-release-https"
	var manifestAsset releaseAsset
	if apiErr == nil {
		if json.Unmarshal(api, &release) != nil || release.Draft || release.Prerelease || !strings.HasPrefix(release.Tag, "v") {
			return updatePlan{}, fault("metadata-unavailable")
		}
		version := strings.TrimPrefix(release.Tag, "v")
		_, preview, err := versionParts(version)
		if err != nil || preview != "" {
			return updatePlan{}, fault("metadata-unavailable")
		}
		manifestAsset, err = singleAsset(release, "git-next-update.json")
		manifestURL = officialURL(version, "git-next-update.json")
		if err != nil || manifestAsset.URL != manifestURL || manifestAsset.Size < 1 || manifestAsset.Size > 1<<20 || !digestPattern.MatchString(strings.TrimPrefix(manifestAsset.Digest, "sha256:")) || !strings.HasPrefix(manifestAsset.Digest, "sha256:") {
			return updatePlan{}, fault("metadata-unavailable")
		}
		trust = "official-api-digest"
	}
	data, err := metadata(manifestURL, 1<<20)
	if err != nil {
		return updatePlan{}, err
	}
	hash := sha256.Sum256(data)
	digest := hex.EncodeToString(hash[:])
	if apiErr == nil && (manifestAsset.Digest != "sha256:"+digest || manifestAsset.Size != int64(len(data))) {
		return updatePlan{}, fault("metadata-unavailable")
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.Schema != 1 || manifest.Product != "git-next" || !manifest.Parity || !commitPattern.MatchString(manifest.Commit) {
		return updatePlan{}, fault("metadata-unavailable")
	}
	newer, err := newerVersion(manifest.Version, installed.Version)
	if err != nil || (apiErr == nil && release.Tag != "v"+manifest.Version) {
		return updatePlan{}, fault("metadata-unavailable")
	}
	entry, ok := manifest.Platforms[installed.Platform]
	name := "git-next-" + manifest.Version + "-" + installed.Platform + ".tar.gz"
	if !ok || entry.Asset != name || entry.Kind != "tar" || entry.Size < 4096 || entry.Size > 1<<30 || !digestPattern.MatchString(entry.SHA) {
		return updatePlan{}, fault("metadata-unavailable")
	}
	url := officialURL(manifest.Version, name)
	if apiErr == nil {
		asset, err := singleAsset(release, name)
		if err != nil || asset.URL != url || asset.Size != entry.Size || asset.Digest != "sha256:"+entry.SHA {
			return updatePlan{}, fault("metadata-unavailable")
		}
	}
	return updatePlan{Version: manifest.Version, Commit: manifest.Commit, URL: url, ManifestSHA: digest, Trust: trust, Entry: entry, Newer: newer}, nil
}

func downloadOrder(plan updatePlan) []string {
	type probe struct {
		url      string
		duration time.Duration
	}
	urls := []string{plan.URL}
	for _, prefix := range mirrorPrefixes {
		urls = append(urls, prefix+plan.URL)
	}
	results := make(chan probe, len(urls))
	for _, raw := range urls {
		go func(raw string) {
			start := time.Now()
			value := probe{url: raw, duration: time.Hour}
			response, err := request(context.Background(), "HEAD", raw, raw == plan.URL, 5*time.Second)
			if err == nil {
				if response.StatusCode >= 200 && response.StatusCode < 300 {
					value.duration = time.Since(start)
				}
				_ = response.Body.Close()
			}
			results <- value
		}(raw)
	}
	var probes []probe
	for range urls {
		probes = append(probes, <-results)
	}
	sort.SliceStable(probes, func(i, j int) bool { return probes[i].duration < probes[j].duration })
	for index, value := range probes {
		urls[index] = value.url
	}
	return urls
}

func download(plan updatePlan, destination string) error {
	for _, raw := range downloadOrder(plan) {
		if err := downloadOne(plan, raw, destination); err == nil {
			return nil
		}
		_ = os.Remove(destination)
	}
	return fault("download-failed")
}

func downloadOne(plan updatePlan, raw, destination string) error {
	response, err := request(context.Background(), "GET", raw, raw == plan.URL, 10*time.Minute)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || (response.ContentLength >= 0 && response.ContentLength != plan.Entry.Size) {
		return fault("download-failed")
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, plan.Entry.Size+1))
	if err != nil || size != plan.Entry.Size || fmt.Sprintf("%x", hash.Sum(nil)) != plan.Entry.SHA {
		return fault("download-failed")
	}
	return file.Sync()
}
