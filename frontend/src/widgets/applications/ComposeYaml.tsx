import { Fragment } from 'react';
import { cn } from '@/lib/utils';

// Lightweight, dependency-free YAML highlighter for the compose viewer. Compose
// YAML is simple and predictable, so a per-line tokenizer keeps the bundle lean
// (no syntax-highlighting library). Long lines soft-wrap — no horizontal scroll.

// indent + optional list marker + key + colon + spacing + value
const KEY_RE = /^(\s*)((?:- )?)([A-Za-z0-9_.\-/]+)(:)(\s*)(.*)$/;
// indent + list marker + value
const LIST_RE = /^(\s*)(- )(.*)$/;

function ValueSpan({ value }: { value: string }) {
  if (value.trim() === '') return null;
  const v = value.trim();
  let cls = 'text-foreground/90';
  if (/^(['"]).*\1$/.test(v)) cls = 'text-emerald-600 dark:text-emerald-300';
  else if (/^(true|false|null|yes|no|on|off)$/i.test(v)) cls = 'text-orange-600 dark:text-orange-300';
  else if (/^-?\d+(\.\d+)?$/.test(v)) cls = 'text-purple-600 dark:text-purple-300';
  else if (v.includes('${')) cls = 'text-amber-600 dark:text-amber-300';
  return <span className={cls}>{value}</span>;
}

function Line({ raw }: { raw: string }) {
  if (raw.trim() === '') return <span> </span>;
  if (raw.trimStart().startsWith('#')) {
    return <span className="italic text-muted-foreground/50">{raw}</span>;
  }
  const km = KEY_RE.exec(raw);
  if (km) {
    const [, indent, listMark, key, colon, space, value] = km;
    return (
      <span>
        {indent}
        {listMark && <span className="text-muted-foreground/60">{listMark}</span>}
        <span className="text-sky-600 dark:text-cyan-300">{key}</span>
        <span className="text-muted-foreground/70">{colon}</span>
        {space}
        <ValueSpan value={value} />
      </span>
    );
  }
  const lm = LIST_RE.exec(raw);
  if (lm) {
    const [, indent, mark, value] = lm;
    return (
      <span>
        {indent}
        <span className="text-muted-foreground/60">{mark}</span>
        <ValueSpan value={value} />
      </span>
    );
  }
  return <span className="text-foreground/90">{raw}</span>;
}

/** Renders compose YAML with soft line-wrapping and basic syntax highlighting. */
export function ComposeYaml({ content, className }: { content: string; className?: string }) {
  const lines = content.replace(/\n+$/, '').split('\n');
  return (
    <pre
      className={cn(
        'max-h-[60vh] overflow-x-hidden overflow-y-auto whitespace-pre-wrap break-all rounded-md bg-muted/40 p-3 font-mono text-[11px] leading-relaxed',
        className
      )}
    >
      {lines.map((l, i) => (
        <Fragment key={i}>
          <Line raw={l} />
          {'\n'}
        </Fragment>
      ))}
    </pre>
  );
}
