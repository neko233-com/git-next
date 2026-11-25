import { useEffect, useRef, useState } from "react";
import { Icon } from "./Icon";

/** 带复制反馈的按钮，代码块和速查表共用。 */
export function CopyButton({ text, compact = false }: { text: string; compact?: boolean }) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<number | undefined>(undefined);

  useEffect(() => () => window.clearTimeout(timer.current), []);

  async function handleCopy() {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      window.clearTimeout(timer.current);
      timer.current = window.setTimeout(() => setCopied(false), 1800);
    } catch {
      setCopied(false);
    }
  }

  return (
    <>
      <button
        type="button"
        className={compact ? "copy-button copy-button-compact" : "copy-button"}
        onClick={handleCopy}
        aria-label={copied ? "已复制" : `复制：${text}`}
      >
        <Icon name={copied ? "check" : "copy"} size={compact ? 14 : 15} />
        <span>{copied ? "已复制" : "复制"}</span>
      </button>
      <span className="sr-only" role="status" aria-live="polite">
        {copied ? "内容已复制到剪贴板" : ""}
      </span>
    </>
  );
}

export function CodeBlock({ code, caption }: { code: string; caption?: string }) {
  return (
    <figure className="code-block">
      {caption ? <figcaption>{caption}</figcaption> : null}
      <div className="code-surface">
        <pre>
          <code>{code}</code>
        </pre>
        <CopyButton text={code} />
      </div>
    </figure>
  );
}
