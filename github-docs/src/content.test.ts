import { describe, expect, it } from "vitest";
import {
  commandCheatSheet,
  searchSections,
  sectionHeadings,
  sections,
  unsupportedCommands,
} from "./content";

describe("文档内容结构", () => {
  it("章节 id 唯一", () => {
    const ids = sections.map((section) => section.id);
    expect(new Set(ids).size).toBe(ids.length);
  });

  it("每个章节都有导航标题、摘要、目录标题和非空步骤", () => {
    for (const section of sections) {
      expect(section.nav.trim().length).toBeGreaterThan(0);
      expect(section.title.trim().length).toBeGreaterThan(0);
      expect(section.summary.trim().length).toBeGreaterThan(0);
      expect(sectionHeadings(section).length).toBeGreaterThan(0);

      for (const block of section.blocks) {
        if (block.kind === "steps") {
          expect(block.items.length).toBeGreaterThan(0);
          for (const item of block.items) {
            expect(item.text.trim().length).toBeGreaterThan(0);
          }
        }
        if (block.kind === "checklist") {
          for (const item of block.items) {
            expect(item.trim().length).toBeGreaterThan(0);
          }
        }
        if (block.kind === "faq") {
          for (const item of block.items) {
            expect(item.question.trim().length).toBeGreaterThan(0);
            expect(item.answer.trim().length).toBeGreaterThan(0);
          }
        }
      }
    }
  });

  it("搜索能命中章节内容", () => {
    expect(searchSections("").length).toBe(0);
    expect(searchSections("大文件").length).toBeGreaterThan(0);
    expect(searchSections("语言").length).toBeGreaterThan(0);
  });
});

describe("常用命令速查表", () => {
  it("不把当前实现没有的命令列为可用命令", () => {
    for (const row of commandCheatSheet) {
      expect(row.command).not.toContain("commit -a");
      expect(row.command).not.toContain("rev-parse --abbrev-ref");
      expect(row.when.trim().length).toBeGreaterThan(0);
    }
  });

  it("明确标出没有的命令，并给出替代做法", () => {
    const listed = unsupportedCommands.map((row) => row.command);
    expect(listed).toContain("git commit -a");
    expect(listed).toContain("git rev-parse --abbrev-ref");
    for (const row of unsupportedCommands) {
      expect(row.why.trim().length).toBeGreaterThan(0);
      expect(row.instead.trim().length).toBeGreaterThan(0);
    }
  });
});
