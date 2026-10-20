// Copyright 2026 git-next contributors. MIT license.
//go:build linux || darwin

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type options struct {
	mode, base, scope, source string
	json, manual              bool
}

type result struct {
	Schema         int    `json:"schema_version"`
	Status         string `json:"status"`
	Version        string `json:"version,omitempty"`
	Current        string `json:"installed_version,omitempty"`
	Scope          string `json:"install_scope,omitempty"`
	Newer          *bool  `json:"newer,omitempty"`
	Trust          string `json:"metadata_trust,omitempty"`
	Enabled        *bool  `json:"enabled,omitempty"`
	Code           string `json:"error,omitempty"`
	CleanupPending bool   `json:"cleanup_pending,omitempty"`
}

type fault string

func (e fault) Error() string { return string(e) }

func main() {
	if err := run(os.Args[1:]); err != nil {
		code := "operation-failed"
		var known fault
		if errors.As(err, &known) {
			code = string(known)
		}
		if code == "worker-restart" {
			os.Exit(75)
		}
		if hasJSON(os.Args[1:]) {
			_ = json.NewEncoder(os.Stdout).Encode(result{Schema: 1, Status: "failed", Code: code})
		} else {
			fmt.Fprintln(os.Stderr, message(code))
		}
		os.Exit(1)
	}
}

func hasJSON(args []string) bool {
	for _, arg := range args {
		if arg == "--json" {
			return true
		}
	}
	return false
}

func run(args []string) error {
	if len(args) == 0 {
		return fault("invalid-arguments")
	}
	o := options{mode: args[0]}
	flags := flag.NewFlagSet("git-next updater", flag.ContinueOnError)
	flags.SetOutput(new(strings.Builder))
	flags.StringVar(&o.base, "base", "", "owned versioned installation root")
	flags.StringVar(&o.scope, "scope", "user", "user or machine; Install only")
	flags.StringVar(&o.source, "package", "", "verified unpacked package; Install only")
	flags.BoolVar(&o.json, "json", false, "stable machine output")
	flags.BoolVar(&o.manual, "manual-updates", false, "explicitly omit service installation")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 {
		return fault("invalid-arguments")
	}
	if o.mode == "Install" {
		return install(o)
	}
	if o.source != "" || o.manual || o.scope != "user" {
		return fault("invalid-arguments")
	}
	if o.base == "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		exe, err = filepath.EvalSymlinks(exe)
		if err != nil {
			return err
		}
		// base/releases/<immutable-id>/libexec/git-next/updater.
		o.base = filepath.Clean(filepath.Join(filepath.Dir(exe), "../../../.."))
	}
	c, err := openContext(o.base)
	if err != nil {
		return err
	}
	defer c.close()
	switch o.mode {
	case "Check":
		plan, err := officialPlan(c.pkg)
		if err != nil {
			return err
		}
		return emit(o, result{Status: "checked", Version: plan.Version, Current: c.pkg.Version, Scope: c.receipt.Scope, Newer: &plan.Newer, Trust: plan.Trust})
	case "Status":
		s, err := c.readState()
		if err != nil {
			return err
		}
		enabled, err := c.enabled()
		if err != nil {
			return err
		}
		return emit(o, result{Status: s.Status, Version: s.Version, Current: c.pkg.Version, Scope: c.receipt.Scope, Enabled: &enabled, Code: s.Error})
	case "RequestApply":
		if err := requestApply(c); err != nil {
			return err
		}
		return emit(o, result{Status: "queued", Scope: c.receipt.Scope})
	case "Enable", "Disable":
		if err := c.writable(); err != nil {
			return err
		}
		lock, err := c.lock("update.lock")
		if err != nil {
			return err
		}
		defer lock.Close()
		enabled := o.mode == "Enable"
		if enabled && c.receipt.Service.Path == "" {
			prior := c.receipt
			c.receipt.Service, err = makeService(c)
			if err != nil {
				return err
			}
			if err := atomicJSON(c.root, "install.json", c.receipt, 0644); err != nil {
				return err
			}
			if err := c.installService(); err != nil {
				_ = c.removeService()
				_ = atomicJSON(c.root, "install.json", prior, 0644)
				return err
			}
		}
		if err := atomicJSON(c.state, "policy.json", policy{Schema: 1, Enabled: enabled}, 0644); err != nil {
			return err
		}
		return emit(o, result{Status: "configured", Scope: c.receipt.Scope, Enabled: &enabled})
	case "Apply", "Scheduled":
		return c.update(o.mode == "Scheduled")
	case "Serve":
		return c.serve()
	case "Rollback":
		return c.rollback()
	default:
		return fault("invalid-arguments")
	}
}

func emit(o options, value result) error {
	value.Schema = 1
	if o.json {
		return json.NewEncoder(os.Stdout).Encode(value)
	}
	fmt.Fprint(os.Stdout, message(value.Status))
	if value.Version != "" {
		fmt.Fprintf(os.Stdout, " (%s)", value.Version)
	}
	if value.Enabled != nil {
		fmt.Fprintf(os.Stdout, " %t", *value.Enabled)
	}
	fmt.Fprintln(os.Stdout)
	return nil
}

func message(key string) string {
	english := map[string]string{
		"checked": "git-next update check completed.", "queued": "git-next update was queued.",
		"configured": "git-next update policy was saved.", "installed": "git-next installation completed.",
		"idle": "git-next updater is ready.", "current": "git-next is current.",
		"updated": "git-next update completed.", "disabled": "git-next automatic updates are disabled.",
		"rolled-back": "git-next previous version was restored.", "failed": "git-next update failed.",
		"invalid-arguments":      "Invalid git-next updater arguments.",
		"invalid-package":        "Package identity, contents or compatibility gate is invalid.",
		"unsafe-path":            "An installation path is not safely owned.",
		"busy":                   "Another git-next update or installation is running.",
		"administrator-required": "Machine update policy and installation require an administrator.",
		"service-unavailable":    "The owned updater service is unavailable; automatic update was not queued.",
		"metadata-unavailable":   "Official GitHub update metadata is unavailable or invalid.",
		"download-failed":        "All verified package transports failed; the current version is unchanged.",
		"no-rollback":            "No verified previous git-next version is available.",
		"operation-failed":       "git-next operation failed; the protected transaction can be recovered.",
	}
	chinese := map[string]string{
		"checked": "git-next 更新检查完成。", "queued": "git-next 更新已排队。",
		"configured": "git-next 更新策略已保存。", "installed": "git-next 安装完成。",
		"idle": "git-next 更新器已就绪。", "current": "git-next 已是最新版本。",
		"updated": "git-next 更新完成。", "disabled": "git-next 自动更新已禁用。",
		"rolled-back": "git-next 已恢复到上一版本。", "failed": "git-next 更新失败。",
		"invalid-arguments": "git-next 更新器参数无效。", "invalid-package": "安装包身份、内容或兼容性门槛无效。",
		"unsafe-path": "安装路径的归属或访问权限不安全。", "busy": "另一个 git-next 更新或安装正在运行。",
		"administrator-required": "机器级安装和更新策略变更需要管理员权限。",
		"service-unavailable":    "自有更新服务不可用，未提交自动更新请求。",
		"metadata-unavailable":   "官方 GitHub 更新元数据不可用或无效。",
		"download-failed":        "所有安装包校验下载均失败，当前版本未改动。",
		"no-rollback":            "没有可校验的 git-next 上一版本。",
		"operation-failed":       "git-next 操作失败，可通过受保护事务恢复。",
	}
	selection := os.Getenv("GIT_NEXT_LANGUAGE")
	if selection == "" || selection == "auto" || selection == "system" {
		for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANGUAGE", "LANG"} {
			if selection = os.Getenv(key); selection != "" {
				break
			}
		}
	}
	if strings.HasPrefix(strings.ToLower(selection), "zh") || selection == "chinese" {
		if value := chinese[key]; value != "" {
			return value
		}
	}
	if value := english[key]; value != "" {
		return value
	}
	return english["operation-failed"]
}
