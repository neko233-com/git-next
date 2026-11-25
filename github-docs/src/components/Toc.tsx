import type { Section } from "../content";
import { sectionHeadings } from "../content";
import { Icon } from "./Icon";

export function Toc({ section }: { section: Section }) {
  const headings = sectionHeadings(section);

  function goTo(anchor: string) {
    const target = document.getElementById(`${section.id}-${anchor}`);
    target?.scrollIntoView({ behavior: "smooth", block: "start" });
    target?.focus({ preventScroll: true });
  }

  return (
    <nav className="toc" aria-label="本页目录">
      <p className="toc-title">
        <Icon name="book" size={15} />
        本页目录
      </p>
      <ul>
        {headings.map((heading) => (
          <li key={heading.anchor}>
            <button type="button" onClick={() => goTo(heading.anchor)}>
              {heading.text}
            </button>
          </li>
        ))}
      </ul>
    </nav>
  );
}
