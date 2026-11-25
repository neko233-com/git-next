import type { Section } from "../content";
import { sections } from "../content";
import { BlockView } from "./BlockView";
import { Icon } from "./Icon";

type ArticleProps = {
  section: Section;
  onNavigate: (id: string) => void;
};

export function Article({ section, onNavigate }: ArticleProps) {
  const index = sections.findIndex((item) => item.id === section.id);
  const previous = sections[index - 1];
  const next = sections[index + 1];

  return (
    <article className="article">
      <header className="article-header">
        <span className="kicker">{section.kicker}</span>
        <h1>{section.title}</h1>
        <p className="summary">{section.summary}</p>
      </header>

      <div className="article-body">
        {section.blocks.map((block, blockIndex) => (
          <BlockView
            key={`${section.id}-block-${blockIndex}`}
            block={block}
            sectionId={section.id}
          />
        ))}
      </div>

      <nav className="article-nav" aria-label="上下篇导航">
        {previous ? (
          <button
            type="button"
            className="article-nav-button"
            onClick={() => onNavigate(previous.id)}
          >
            <span>上一篇</span>
            <strong>{previous.nav}</strong>
          </button>
        ) : (
          <span />
        )}
        {next ? (
          <button
            type="button"
            className="article-nav-button article-nav-next"
            onClick={() => onNavigate(next.id)}
          >
            <span>下一篇</span>
            <strong>{next.nav}</strong>
            <Icon name="arrow" size={16} />
          </button>
        ) : (
          <span />
        )}
      </nav>
    </article>
  );
}
