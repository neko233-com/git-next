import { useCallback, useEffect, useState } from "react";
import { findSection, sections } from "./content";
import { Article } from "./components/Article";
import { Icon } from "./components/Icon";
import { Sidebar } from "./components/Sidebar";
import { Toc } from "./components/Toc";

type Theme = "light" | "dark";

const THEME_KEY = "git-next-docs-theme";

function readHash(): string {
  const raw = window.location.hash.replace(/^#\/?/, "").split("/")[0];
  return sections.some((section) => section.id === raw) ? raw : "home";
}

function readTheme(): Theme {
  try {
    return window.localStorage.getItem(THEME_KEY) === "dark" ? "dark" : "light";
  } catch {
    return "light";
  }
}

export default function App() {
  const [currentId, setCurrentId] = useState(readHash);
  const [theme, setTheme] = useState<Theme>(readTheme);
  const [mobileOpen, setMobileOpen] = useState(false);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    try {
      window.localStorage.setItem(THEME_KEY, theme);
    } catch {
      // 浏览器禁用存储时保持本次会话的主题即可
    }
  }, [theme]);

  useEffect(() => {
    if (!window.location.hash) {
      window.history.replaceState(null, "", "#/home");
    }
    const handleHashChange = () => {
      setCurrentId(readHash());
      window.scrollTo({ top: 0, behavior: "smooth" });
    };
    window.addEventListener("hashchange", handleHashChange);
    return () => window.removeEventListener("hashchange", handleHashChange);
  }, []);

  const navigate = useCallback((id: string) => {
    if (window.location.hash !== `#/${id}`) {
      window.location.hash = `#/${id}`;
    }
    setCurrentId(id);
    setMobileOpen(false);
    window.scrollTo({ top: 0, behavior: "smooth" });
  }, []);

  const toggleTheme = useCallback(() => {
    setTheme((current) => (current === "dark" ? "light" : "dark"));
  }, []);

  const section = findSection(currentId);

  return (
    <div className="app-shell">
      <a className="skip-link" href="#main">
        跳到正文
      </a>

      <header className="mobile-header">
        <button
          type="button"
          className="menu-button"
          aria-label="打开导航菜单"
          aria-expanded={mobileOpen}
          onClick={() => setMobileOpen(true)}
        >
          <Icon name="menu" size={20} />
        </button>
        <strong>git-next 使用文档</strong>
        <button
          type="button"
          className="theme-toggle-mobile"
          onClick={toggleTheme}
          aria-label={theme === "dark" ? "切换到浅色" : "切换到深色"}
          aria-pressed={theme === "dark"}
        >
          <Icon name={theme === "dark" ? "sun" : "moon"} size={18} />
        </button>
      </header>

      <Sidebar
        currentId={currentId}
        onNavigate={navigate}
        theme={theme}
        onToggleTheme={toggleTheme}
        mobileOpen={mobileOpen}
        onCloseMobile={() => setMobileOpen(false)}
      />

      {mobileOpen ? (
        <button
          type="button"
          className="sidebar-backdrop"
          aria-label="关闭导航菜单"
          onClick={() => setMobileOpen(false)}
        />
      ) : null}

      <div className="page">
        <div className="content-layout">
          <main id="main" className="main" tabIndex={-1}>
            <Article key={section.id} section={section} onNavigate={navigate} />
          </main>
          <Toc section={section} />
        </div>
        <footer className="page-footer">
          <span>git-next 文档站 · GitHub Pages</span>
          <span>遇到问题请看“常见问题”与“版本与边界”</span>
        </footer>
      </div>
    </div>
  );
}
