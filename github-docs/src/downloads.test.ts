import { describe, expect, it } from "vitest";
import {
  allDownloads,
  checksumDownloads,
  gitNextDownloads,
  previewDownloads,
  terminalDownloads,
} from "./downloads";

describe("下载中心资产", () => {
  it("每个链接都是 GitHub 的 https 地址", () => {
    expect(allDownloads.length).toBeGreaterThan(0);
    for (const asset of allDownloads) {
      expect(asset.href).toMatch(/^https:\/\//);
      expect(asset.href).toContain("github.com/neko233-com/");
      expect(asset.href).toContain("/releases/latest/download/");
      expect(asset.href).not.toContain(" ");
    }
  });

  it("资产 id 唯一", () => {
    const ids = allDownloads.map((asset) => asset.id);
    expect(new Set(ids).size).toBe(ids.length);
  });

  it("包含 5 个 git-next 平台与 2 个终端平台", () => {
    expect(gitNextDownloads.map((asset) => asset.id)).toEqual([
      "git-next-windows-x64",
      "git-next-windows-arm64",
      "git-next-macos-arm64",
      "git-next-linux-x64",
      "git-next-linux-arm64",
    ]);
    expect(terminalDownloads.map((asset) => asset.id)).toEqual([
      "terminal-windows-x64",
      "terminal-windows-arm64",
      "terminal-source",
      "terminal-install-bundle",
    ]);
    expect(terminalDownloads.filter((asset) => asset.kind === "archive")).toHaveLength(2);
  });

  it("直链的文件名与展示文案一致", () => {
    for (const asset of allDownloads) {
      expect(asset.href.endsWith(`/${asset.detail}`)).toBe(true);
      expect(asset.label.trim().length).toBeGreaterThan(0);
    }
  });

  it("两个 Release 都提供校验文件", () => {
    expect(checksumDownloads.map((asset) => asset.id)).toEqual([
      "git-next-checksums",
      "terminal-checksums",
    ]);
  });

  it("Go-only 预览包指向本站 downloads 目录并带校验文件", () => {
    expect(previewDownloads.map((asset) => asset.id)).toEqual([
      "go-preview-windows-x64",
      "go-preview-checksums",
    ]);
    for (const asset of previewDownloads) {
      expect(asset.href).toMatch(/^https:\/\/neko233-com\.github\.io\/git-next\/downloads\//);
      expect(asset.href.endsWith(`/${asset.detail}`)).toBe(true);
    }
  });
});
