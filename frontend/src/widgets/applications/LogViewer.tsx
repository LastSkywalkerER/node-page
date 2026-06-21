import { useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { toast } from 'sonner';
import { Copy, Search, X, ChevronUp, ChevronDown } from 'lucide-react';
import { cn } from '@/lib/utils';
import { copyToClipboard } from '@/shared/lib/clipboard';

const ERROR_RE = /\b(FATA|FATAL|ERRO|ERROR|ERR)\b/;
const WARN_RE = /\b(WARN|WRN)\b/;
const INFO_RE = /\b(INFO|INF)\b/;
const DEBUG_RE = /\b(DEBU|DEBUG|DBG|TRACE|TRC)\b/;

// Split a line into optional [docker-ts] [app date-time] [LEVEL] [rest].
const LINE_RE = /^(\S+Z)?\s*(\d{4}\/\d{2}\/\d{2}\s+\d{2}:\d{2}:\d{2}(?:\.\d+)?)?\s*([A-Z]{3,5})?\s*([\s\S]*)$/;

function lineClass(line: string): string {
  if (ERROR_RE.test(line)) return 'text-red-400';
  if (WARN_RE.test(line)) return 'text-amber-400';
  if (INFO_RE.test(line)) return 'text-emerald-400/90';
  if (DEBUG_RE.test(line)) return 'text-sky-300/70';
  return 'text-foreground/80';
}

function isLevel(tok: string): boolean {
  return ERROR_RE.test(tok) || WARN_RE.test(tok) || INFO_RE.test(tok) || DEBUG_RE.test(tok);
}

function countOcc(haystack: string, needle: string): number {
  if (!needle) return 0;
  let n = 0;
  let i = haystack.indexOf(needle);
  while (i >= 0) {
    n++;
    i = haystack.indexOf(needle, i + needle.length);
  }
  return n;
}

/** Render `text`, wrapping each match of `query` in a mark. The match at the
 *  global `activeIndex` gets the active style and the scroll ref. */
function Highlighted({
  text,
  query,
  base,
  activeIndex,
  activeRef,
}: {
  text: string;
  query: string;
  base: number;
  activeIndex: number;
  activeRef: React.RefObject<HTMLElement | null>;
}): ReactNode {
  if (!query) return text;
  const lower = text.toLowerCase();
  const ql = query.toLowerCase();
  const parts: ReactNode[] = [];
  let from = 0;
  let i = lower.indexOf(ql, 0);
  let k = 0;
  while (i >= 0) {
    if (i > from) parts.push(text.slice(from, i));
    const ord = base + k;
    const active = ord === activeIndex;
    parts.push(
      <mark
        key={k}
        ref={active ? (activeRef as React.RefObject<HTMLElement>) : undefined}
        className={cn('rounded-sm', active ? 'bg-amber-400 text-black' : 'bg-amber-400/30 text-foreground')}
      >
        {text.slice(i, i + query.length)}
      </mark>
    );
    from = i + query.length;
    k += 1;
    i = lower.indexOf(ql, from);
  }
  if (from < text.length) parts.push(text.slice(from));
  return parts;
}

function LogLine({
  line,
  query,
  base,
  activeIndex,
  activeRef,
}: {
  line: string;
  query: string;
  base: number;
  activeIndex: number;
  activeRef: React.RefObject<HTMLElement | null>;
}) {
  const cls = lineClass(line);

  // Without an active search, render the pretty split (dim timestamp, bold level).
  if (!query) {
    const m = LINE_RE.exec(line);
    const ts = [m?.[1], m?.[2]].filter(Boolean).join(' ');
    const level = m?.[3] && isLevel(m[3]) ? m[3] : '';
    const rest = level ? (m?.[4] ?? '') : line.slice(ts.length).trimStart() || line;
    return (
      <div className="whitespace-pre-wrap break-all px-3 py-px leading-relaxed">
        {ts && <span className="text-muted-foreground/40">{ts} </span>}
        {level && <span className={cn('font-semibold', cls)}>{level} </span>}
        <span className={cls}>{rest}</span>
      </div>
    );
  }

  // Search mode: whole-line coloured by level with match marks.
  return (
    <div className="whitespace-pre-wrap break-all px-3 py-px leading-relaxed">
      <span className={cls}>
        <Highlighted text={line} query={query} base={base} activeIndex={activeIndex} activeRef={activeRef} />
      </span>
    </div>
  );
}

interface LogViewerProps {
  logs: string;
  loading?: boolean;
}

export function LogViewer({ logs, loading }: LogViewerProps) {
  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);
  const activeRef = useRef<HTMLElement | null>(null);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  // Terminal-style "stick to bottom": auto-scroll to the newest line on each
  // refresh, unless the user has scrolled up to read history.
  const stickRef = useRef(true);

  const lines = useMemo(() => (logs ? logs.replace(/\n+$/, '').split('\n') : []), [logs]);

  const onScroll = () => {
    const el = scrollRef.current;
    if (el) stickRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
  };

  // Follow the tail when new logs arrive (and on first load), unless searching —
  // active-match navigation owns scrolling while a query is set.
  useEffect(() => {
    if (query) return;
    const el = scrollRef.current;
    if (el && stickRef.current) el.scrollTop = el.scrollHeight;
  }, [logs, loading, query]);

  // Per-line match counts → cumulative base offsets → total matches.
  const { bases, total } = useMemo(() => {
    const q = query.toLowerCase();
    const b: number[] = [];
    let sum = 0;
    for (const l of lines) {
      b.push(sum);
      sum += q ? countOcc(l.toLowerCase(), q) : 0;
    }
    return { bases: b, total: sum };
  }, [lines, query]);

  // Reset/clamp the active match when the query or result set changes.
  useEffect(() => {
    setActive(0);
  }, [query]);
  useEffect(() => {
    activeRef.current?.scrollIntoView({ block: 'center', behavior: 'smooth' });
  }, [active, query]);

  const step = (dir: 1 | -1) => {
    if (total === 0) return;
    setActive((a) => (a + dir + total) % total);
  };

  const copy = async () => {
    if (await copyToClipboard(logs)) {
      toast.success('Logs copied to clipboard');
    } else {
      toast.error('Copy failed');
    }
  };

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <div className="relative flex-1">
          <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground/60" />
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault();
                step(e.shiftKey ? -1 : 1);
              }
            }}
            placeholder="Search logs…"
            className="w-full rounded-md border border-border/60 bg-background py-1 pl-7 pr-7 text-xs outline-none focus-visible:ring-1 focus-visible:ring-ring dark:border-white/10"
          />
          {query && (
            <button
              type="button"
              onClick={() => setQuery('')}
              className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground/60 transition-colors hover:text-foreground"
              aria-label="Clear search"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          )}
        </div>

        {query && (
          <div className="flex shrink-0 items-center gap-0.5">
            <span className="w-12 text-right font-mono text-[10px] tabular-nums text-muted-foreground">
              {total ? `${active + 1}/${total}` : '0/0'}
            </span>
            <button
              type="button"
              onClick={() => step(-1)}
              disabled={total === 0}
              className="rounded p-1 text-muted-foreground transition-colors hover:text-foreground disabled:opacity-40"
              aria-label="Previous match"
            >
              <ChevronUp className="h-3.5 w-3.5" />
            </button>
            <button
              type="button"
              onClick={() => step(1)}
              disabled={total === 0}
              className="rounded p-1 text-muted-foreground transition-colors hover:text-foreground disabled:opacity-40"
              aria-label="Next match"
            >
              <ChevronDown className="h-3.5 w-3.5" />
            </button>
          </div>
        )}

        <span className="shrink-0 font-mono text-[10px] tabular-nums text-muted-foreground">{lines.length} lines</span>
        <button
          type="button"
          onClick={copy}
          disabled={!logs}
          className="inline-flex shrink-0 items-center gap-1 rounded-md border border-border/60 px-2 py-1 text-xs text-muted-foreground transition-colors hover:text-foreground disabled:opacity-50 dark:border-white/10"
        >
          <Copy className="h-3.5 w-3.5" /> Copy
        </button>
      </div>

      <div
        ref={scrollRef}
        onScroll={onScroll}
        className="max-h-[60vh] overflow-auto rounded-md bg-muted/40 font-mono text-[11px]"
      >
        {loading ? (
          <p className="p-3 text-muted-foreground">Loading…</p>
        ) : lines.length === 0 ? (
          <p className="p-3 text-muted-foreground">No log output.</p>
        ) : (
          lines.map((l, i) => (
            <LogLine key={i} line={l} query={query} base={bases[i]} activeIndex={active} activeRef={activeRef} />
          ))
        )}
      </div>
    </div>
  );
}
