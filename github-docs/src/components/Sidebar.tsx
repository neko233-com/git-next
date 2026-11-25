import { useState } from "react";
import type { Section } from "../content";
import { findSection, navGroups, searchSections } from "../content";
import { Icon } from "./Icon";

type Theme = "light" | "dark";

type SidebarProps = {
  currentId: string;
  onNavigate: (id: string) => void;
  theme: Theme;
  onToggleTheme: () => void;
  mobileOpen: boolean;
  onCloseMobile: () => void;
};

export function Sidebar({
  currentId,
  onNavigate,
  theme,
  onToggleTheme,
  mobileOpen,
  onCloseMobile,
}: SidebarProps) {
  const [query, setQuery] = useState("");
  const results: Section[] = searchSections(query);

  function navigate(id: string) {
    setQuery("");
    onNavigate(id);
  }

  return (
    <aside className={mobileOpen ? "sidebar sidebar-open" : "sidebar"} aria-label="文档导航">
      <div className="brand">
        <span className="brand-mark" aria-hidden="true">
          gn
        </span>
        <span className="brand-text">
          <strong>git-next</strong>
          <small>使用文档</small>
        </span>
      </div>

      <div className="search-box">
        <Icon name="search" size={17} />
        <input
          type="search"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="搜索文档内容"
          aria-label="搜索文档内容"
        />
        {query ? (
          <button
            type="button"
            className="search-clear"
            onClick={() => setQuery("")}
            aria-label="清空搜索"
          >
            <Icon name="close" size={14} />
          </button>
        ) : null}
      </div>

      {query ? (
        <div className="search-results" aria-live="polite">
          <p className="search-meta">
            {results.length ? `找到 ${results.length} 个结果` : "没有找到相关内容"}
          </p>
          <ul>
            {results.map((section) => (
              <li key={section.id}>
                <button type="button" onClick={() => navigate(section.id)}>
                  <strong>{section.nav}</strong>
                  <span>{section.title}</span>
                </button>
              </li>
            ))}
          </ul>
        </div>
      ) : (
        <nav className="nav-groups" aria-label="章节导航">
          {navGroups.map((group) => (
            <div className="nav-group" key={group.name}>
              <p className="nav-group-title">{group.name}</p>
              <ul>
                {group.ids.map((id) => {
                  const section = findSection(id);
                  const active = id === currentId;
                  return (
                    <li key={id}>
                      <button
                        type="button"
                        className={active ? "nav-link nav-link-active" : "nav-link"}
                        aria-current={active ? "page" : undefined}
                        onClick={() => navigate(id)}
                      >
                        <Icon name="book" size={15} />
                        <span>{section.nav}</span>
                      </button>
                    </li>
                  );
                })}
              </ul>
            </div>
          ))}
        </nav>
      )}

      <div className="sidebar-footer">
        <button
          type="button"
          className="theme-toggle"
          onClick={onToggleTheme}
          aria-pressed={theme === "dark"}
        >
          <Icon name={theme === "dark" ? "sun" : "moon"} size={17} />
          <span>{theme === "dark" ? "切换到浅色" : "切换到深色"}</span>
        </button>
        <button
          type="button"
          className="mobile-close"
          onClick={onCloseMobile}
          aria-label="关闭导航菜单"
        >
          <Icon name="close" size={18} />
        </button>
      </div>
    </aside>
  );
}
