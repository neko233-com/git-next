// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"fmt"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
	htmlparse "golang.org/x/net/html"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type manual struct{ source, page, title string }

var manuals = []manual{
	{"README.md", "index.html", "git-next"},
	{"docs/COMPATIBILITY.md", "compatibility.html", "Compatibility"},
	{"docs/PARITY_ROADMAP.md", "parity-roadmap.html", "Parity roadmap"},
	{"docs/INSTALLATION.md", "installation.html", "Installation"},
	{"docs/BUILDING.md", "building.html", "Builds"},
	{"docs/PERFORMANCE.md", "performance.html", "Performance"},
	{"docs/ACCEPTANCE.md", "acceptance.html", "Acceptance"},
	{"docs/GIT_NEXT_ACCEPTANCE.md", "git-next-acceptance.html", "Rust CLI acceptance"},
	{"docs/TEST_COVERAGE.md", "test-coverage.html", "Test coverage"},
	{"docs/TAKEOVER.md", "takeover.html", "Complete takeover"},
	{"docs/LFS.md", "lfs.html", "Native LFS"},
	{"docs/TERMINAL.md", "terminal.html", "Git-Terminal"},
	{"docs/GAME_PROJECTS.md", "game-projects.html", "Game projects"},
	{"docs/READ_COMMANDS.md", "read-commands.html", "Read commands"},
	{"docs/RESTORATION.md", "restoration.html", "Restore and reset"},
	{"docs/LINE_ENDINGS.md", "line-endings.html", "LF policy"},
	{"docs/NATIVE_DISCOVERY.md", "native-discovery.html", "Native discovery"},
	{"docs/CREDENTIALS.md", "credentials.html", "Credential support"},
	{"docs/MERGE.md", "merge.html", "Native merge"},
	{"docs/UPDATES.md", "updates.html", "MSI and updates"},
	{"docs/LANGUAGE.md", "language.html", "Interface languages"},
	{"docs/NATIVE_CORE.md", "native-core.html", "Native core integration"},
	{"docs/AGENT_WORKFLOW.md", "agents.html", "Engineering workflow"},
	{"docs/DELIVERY_STATUS.md", "delivery-status.html", "Delivery progress"},
	{"docs/CORRESPONDING_SOURCES.md", "corresponding-sources.html", "Runtime sources and licenses"},
	{"docs/DOCUMENTATION_SITE.md", "documentation-site.html", "Documentation site"},
	{"docs/GIT-GUIDE.md", "git-guide.html", "Git guide"},
	{"docs/PRODUCTION-READINESS.md", "production-readiness.html", "Production readiness"},
	{"docs/CLONE-BENCHMARK.md", "clone-benchmark.html", "Clone measurement"},
	{"docs/MSI-PROOF.md", "msi-proof.html", "Installer proof"},
	{"docs/WHY-GIT-NEXT.md", "why-git-next.html", "Why git-next"},
	{"docs/DEFAULTS.md", "defaults.html", "Runtime defaults"},
	{"docs/GO_TOOLING.md", "go-tooling.html", "Go tooling migration"},
}

func init() {
	register("render-docs", "Render manuals and verify every local HTML link", "", renderDocs)
	register("publish-docs", "Publish manuals through a temporary Git index", "dry-run?", publishDocs)
}
func renderLink(root, source, value string, outputs map[string]string) (string, error) {
	u, e := url.Parse(value)
	if e != nil {
		return "", e
	}
	if u.IsAbs() || u.Host != "" || u.Path == "" {
		return value, nil
	}
	target := filepath.Clean(filepath.Join(filepath.Dir(source), filepath.FromSlash(u.Path)))
	if page := outputs[target]; page != "" {
		u.Path = page
	} else {
		rel, e := filepath.Rel(filepath.Join(root, "docs/site"), target)
		if e != nil {
			return "", e
		}
		u.Path = filepath.ToSlash(rel)
	}
	u.RawPath = ""
	return u.String(), nil
}
func rewriteLinks(input []byte, root, source string, outputs map[string]string) ([]byte, error) {
	z := htmlparse.NewTokenizer(bytes.NewReader(input))
	var out bytes.Buffer
	for {
		kind := z.Next()
		if kind == htmlparse.ErrorToken {
			if e := z.Err(); e != io.EOF {
				return nil, e
			}
			return out.Bytes(), nil
		}
		if kind == htmlparse.StartTagToken || kind == htmlparse.SelfClosingTagToken {
			token := z.Token()
			for i := range token.Attr {
				if token.Attr[i].Key == "href" {
					v, e := renderLink(root, source, token.Attr[i].Val, outputs)
					if e != nil {
						return nil, e
					}
					token.Attr[i].Val = v
				}
			}
			out.WriteString(token.String())
		} else {
			out.Write(z.Raw())
		}
	}
}
func verifyHTMLLinks(site, page string, data []byte) error {
	z := htmlparse.NewTokenizer(bytes.NewReader(data))
	for {
		kind := z.Next()
		if kind == htmlparse.ErrorToken {
			if e := z.Err(); e != io.EOF {
				return e
			}
			return nil
		}
		if kind != htmlparse.StartTagToken && kind != htmlparse.SelfClosingTagToken {
			continue
		}
		for _, attr := range z.Token().Attr {
			if attr.Key != "href" && attr.Key != "src" {
				continue
			}
			u, e := url.Parse(attr.Val)
			if e != nil {
				return e
			}
			if u.IsAbs() || u.Host != "" || u.Path == "" {
				continue
			}
			path := filepath.Join(site, filepath.FromSlash(u.Path))
			if !exists(path) {
				return fmt.Errorf("%s: broken local link %q", page, attr.Val)
			}
		}
	}
}
func renderDocs(a *app, _ options) error {
	site := a.path("docs/site")
	outputs := map[string]string{}
	for _, m := range manuals {
		outputs[a.path(m.source)] = m.page
	}
	md := goldmark.New(goldmark.WithExtensions(extension.Table), goldmark.WithRendererOptions(html.WithUnsafe()))
	pages := map[string][]byte{}
	for _, m := range manuals {
		source := a.path(m.source)
		raw, e := os.ReadFile(source)
		if e != nil {
			return e
		}
		template := filepath.Join(site, m.page)
		if !exists(template) {
			template = filepath.Join(site, "index.html")
		}
		old, e := read(template)
		if e != nil {
			return e
		}
		prefix, _, ok := strings.Cut(old, "<main>")
		if !ok {
			return fmt.Errorf("%s has no main template", template)
		}
		prefix += "<main>"
		prefix = regexp.MustCompile(`<title>.*?</title>`).ReplaceAllString(prefix, "<title>git-next · "+htmlparse.EscapeString(m.title)+"</title>")
		for _, link := range manuals {
			if link.page != "index.html" && !strings.Contains(prefix, `href="`+link.page+`"`) {
				prefix = strings.Replace(prefix, "</nav>", `<a href="`+link.page+`">`+htmlparse.EscapeString(link.title)+`</a></nav>`, 1)
			}
		}
		var body bytes.Buffer
		if e = md.Convert(raw, &body); e != nil {
			return e
		}
		rendered, e := rewriteLinks(body.Bytes(), a.root, source, outputs)
		if e != nil {
			return e
		}
		pages[m.page] = append(append([]byte(prefix), rendered...), []byte("</main></html>\n")...)
	}
	for page, data := range pages {
		if e := write(filepath.Join(site, page), data, 0644); e != nil {
			return e
		}
	}
	for page, data := range pages {
		if e := verifyHTMLLinks(site, page, data); e != nil {
			return e
		}
	}
	fmt.Fprintf(a.out, "Rendered %d HTML manuals; all local links resolve.\n", len(pages))
	return nil
}
func publishDocs(a *app, o options) error {
	if e := renderDocs(a, options{}); e != nil {
		return e
	}
	if o.yes("dry-run") {
		return a.json(record{"status": "preview", "pages": len(manuals), "branch": "gh-pages", "refs_changed": false, "pushed": false})
	}
	temp, e := os.MkdirTemp("", "git-next-docs-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(temp)
	stage := filepath.Join(temp, "site")
	if e = os.Mkdir(stage, 0755); e != nil {
		return e
	}
	entries, e := os.ReadDir(a.path("docs/site"))
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			if e = copyFile(a.path("docs/site", entry.Name()), filepath.Join(stage, entry.Name())); e != nil {
				return e
			}
		}
	}
	if e = write(filepath.Join(stage, ".nojekyll"), nil, 0644); e != nil {
		return e
	}
	ctx, cancel := deadline()
	defer cancel()
	env := append(os.Environ(), "GIT_INDEX_FILE="+filepath.Join(temp, "index"))
	git := func(args ...string) (string, error) {
		r, e := runProcess(ctx, a.root, env, nil, "git", args...)
		if e != nil {
			return "", e
		}
		if r.code != 0 {
			return "", fmt.Errorf("git %s failed: %s", args[0], r.stderr)
		}
		return strings.TrimSpace(string(r.stdout)), nil
	}
	parent := ""
	for _, ref := range []string{"refs/heads/gh-pages", "refs/remotes/origin/gh-pages"} {
		r, e := a.run("git", "rev-parse", "--verify", ref)
		if e == nil && r.code == 0 {
			parent = strings.TrimSpace(string(r.stdout))
			break
		}
	}
	if parent == "" {
		_, _ = a.run("git", "fetch", "origin", "gh-pages:refs/remotes/origin/gh-pages")
		r, e := a.run("git", "rev-parse", "--verify", "refs/remotes/origin/gh-pages")
		if e == nil && r.code == 0 {
			parent = strings.TrimSpace(string(r.stdout))
		}
	}
	if _, e = git("read-tree", "--empty"); e != nil {
		return e
	}
	if _, e = git("--work-tree", stage, "add", "--all", "--force"); e != nil {
		return e
	}
	tree, e := git("write-tree")
	if e != nil {
		return e
	}
	args := []string{"commit-tree", tree, "-m", "docs: publish the manual\n\nAssisted-by: Codex: GPT-6"}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	commit, e := git(args...)
	if e != nil {
		return e
	}
	if _, e = git("update-ref", "refs/heads/gh-pages", commit); e != nil {
		return e
	}
	if _, e = git("push", "origin", "refs/heads/gh-pages:refs/heads/gh-pages"); e != nil {
		return e
	}
	return a.json(record{"status": "published", "commit": commit, "branch": "gh-pages"})
}
