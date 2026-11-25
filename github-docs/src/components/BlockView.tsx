import type { Block, CalloutTone } from "../content";
import { commandCheatSheet, unsupportedCommands } from "../content";
import { CodeBlock, CopyButton } from "./CodeBlock";
import { Icon, type IconName } from "./Icon";

const calloutIcon: Record<CalloutTone, IconName> = {
  info: "info",
  warn: "warn",
  danger: "danger",
};

function CheatSheet() {
  return (
    <div className="table-wrap">
      <table className="data-table">
        <caption className="sr-only">常用命令及用途</caption>
        <thead>
          <tr>
            <th scope="col">命令</th>
            <th scope="col">什么时候用</th>
            <th scope="col">提醒</th>
            <th scope="col">
              <span className="sr-only">复制</span>
            </th>
          </tr>
        </thead>
        <tbody>
          {commandCheatSheet.map((row) => (
            <tr key={row.command}>
              <td>
                <code className="inline-code">{row.command}</code>
              </td>
              <td>{row.when}</td>
              <td>{row.note}</td>
              <td className="cell-copy">
                <CopyButton text={row.command} compact />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function UnsupportedList() {
  return (
    <div className="table-wrap">
      <table className="data-table data-table-warn">
        <caption className="sr-only">当前实现不支持的命令与替代做法</caption>
        <thead>
          <tr>
            <th scope="col">不要写这条</th>
            <th scope="col">为什么</th>
            <th scope="col">替代做法</th>
          </tr>
        </thead>
        <tbody>
          {unsupportedCommands.map((row) => (
            <tr key={row.command}>
              <td>
                <code className="inline-code strikethrough">{row.command}</code>
              </td>
              <td>{row.why}</td>
              <td>{row.instead}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function BlockView({ block, sectionId }: { block: Block; sectionId: string }) {
  switch (block.kind) {
    case "heading":
      return (
        <h2 id={`${sectionId}-${block.anchor}`} className="heading" tabIndex={-1}>
          {block.text}
        </h2>
      );

    case "paragraph":
      return <p className="paragraph">{block.text}</p>;

    case "steps":
      return (
        <ol className="steps">
          {block.items.map((item, index) => (
            <li key={`${sectionId}-step-${index}`} className="step">
              <span className="step-number" aria-hidden="true">
                {index + 1}
              </span>
              <div className="step-body">
                <p>{item.text}</p>
                {item.command ? <CodeBlock code={item.command} /> : null}
                {item.note ? <p className="step-note">{item.note}</p> : null}
              </div>
            </li>
          ))}
        </ol>
      );

    case "code":
      return <CodeBlock code={block.code} caption={block.caption} />;

    case "callout":
      return (
        <aside className={`callout callout-${block.tone}`} aria-label={block.title}>
          <span className="callout-icon">
            <Icon name={calloutIcon[block.tone]} size={20} />
          </span>
          <div className="callout-body">
            <strong>{block.title}</strong>
            <p>{block.text}</p>
          </div>
        </aside>
      );

    case "list": {
      const items = block.items.map((item, index) => (
        <li key={`${sectionId}-li-${index}`}>{item}</li>
      ));
      return block.ordered ? (
        <ol className="text-list">{items}</ol>
      ) : (
        <ul className="text-list">{items}</ul>
      );
    }

    case "checklist":
      return (
        <ul className="checklist">
          {block.items.map((item, index) => (
            <li key={`${sectionId}-check-${index}`}>
              <label>
                <input type="checkbox" />
                <span>{item}</span>
              </label>
            </li>
          ))}
        </ul>
      );

    case "faq":
      return (
        <div className="faq-list">
          {block.items.map((item, index) => (
            <details key={`${sectionId}-faq-${index}`} className="faq-item" open={index === 0}>
              <summary>
                <Icon name="arrow" size={16} />
                <span>{item.question}</span>
              </summary>
              <div className="faq-answer">
                <p>{item.answer}</p>
                {item.command ? <CodeBlock code={item.command} /> : null}
              </div>
            </details>
          ))}
        </div>
      );

    case "cheatsheet":
      return <CheatSheet />;

    case "unsupported":
      return <UnsupportedList />;
  }
}
