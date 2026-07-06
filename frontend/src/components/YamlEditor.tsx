import { useMemo, useRef, type ReactNode } from 'react';
import { parseDocument, LineCounter } from 'yaml';

export interface YamlIssue {
  /** 1-based line, 0 = document-level. */
  line: number;
  message: string;
}

/** Result of one parse: the JS value (undefined on error) + issues. */
export interface YamlParseResult {
  value: unknown;
  issues: YamlIssue[];
}

/**
 * parseYaml turns editor text into a JS value with line-anchored issues,
 * running the caller's semantic validation on top of the syntax check.
 * Exported for reuse (form submission) and unit tests.
 */
export function parseYaml(
  text: string,
  validate?: (value: unknown) => YamlIssue[],
): YamlParseResult {
  const counter = new LineCounter();
  const doc = parseDocument(text, { lineCounter: counter });
  const issues: YamlIssue[] = doc.errors.map((err) => ({
    line: err.pos.length > 0 ? counter.linePos(err.pos[0]).line : 0,
    // err.message repeats the position; keep the first sentence.
    message: err.message.split(' at line')[0],
  }));
  if (issues.length > 0) {
    return { value: undefined, issues };
  }
  const value: unknown = doc.toJS();
  return { value, issues: validate ? validate(value) : [] };
}

// Minimal YAML syntax highlighting: line comments, keys, and scalar
// classes. Regex-per-line is enough for config-sized documents and keeps
// the page free of a full editor dependency (CSP forbids CDNs anyway).
function highlight(line: string): ReactNode {
  const comment = line.indexOf('#');
  // Naive comment split is fine for config docs (quoted '#' is rare and
  // only loses coloring, never content).
  const code = comment >= 0 ? line.slice(0, comment) : line;
  const trailer = comment >= 0 ? line.slice(comment) : '';
  const keyMatch = /^(\s*(?:- )?)([^\s:#][^:#]*)(:)(\s|$)/.exec(code);
  const nodes: ReactNode[] = [];
  if (keyMatch) {
    const [, indent, key, colon] = keyMatch;
    const rest = code.slice(indent.length + key.length + colon.length);
    nodes.push(
      indent,
      <span key="k" className="text-sky-700 dark:text-sky-300">
        {key}
      </span>,
      colon,
      <span key="v" className="text-amber-700 dark:text-amber-200">
        {rest}
      </span>,
    );
  } else {
    nodes.push(
      <span key="v" className="text-amber-700 dark:text-amber-200">
        {code}
      </span>,
    );
  }
  if (trailer) {
    nodes.push(
      <span key="c" className="text-slate-400 dark:text-slate-500">
        {trailer}
      </span>,
    );
  }
  return nodes;
}

/**
 * YAML editor: transparent textarea over a highlighted mirror, gutter
 * with line numbers, and live syntax + schema validation. The caller
 * owns the text state and receives parse results through `issues`
 * computed by parseYaml — call parseYaml again on submit for the value.
 */
export function YamlEditor({
  value,
  onChange,
  rows = 16,
  validate,
}: {
  value: string;
  onChange: (text: string) => void;
  rows?: number;
  validate?: (value: unknown) => YamlIssue[];
}) {
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const mirrorRef = useRef<HTMLDivElement>(null);

  const { issues } = useMemo(() => parseYaml(value, validate), [value, validate]);
  const badLines = useMemo(() => new Set(issues.map((i) => i.line)), [issues]);
  const lines = value.split('\n');

  const syncScroll = () => {
    if (mirrorRef.current && textareaRef.current) {
      mirrorRef.current.scrollTop = textareaRef.current.scrollTop;
      mirrorRef.current.scrollLeft = textareaRef.current.scrollLeft;
    }
  };

  return (
    <div>
      <div
        className="relative overflow-hidden rounded-md border border-slate-300 bg-white font-mono text-xs leading-5 dark:border-slate-600 dark:bg-slate-900"
        style={{ height: `${rows * 1.25 + 1.5}rem` }}
      >
        {/* Highlighted mirror (behind the transparent textarea). */}
        <div
          ref={mirrorRef}
          aria-hidden
          className="pointer-events-none absolute inset-0 overflow-hidden whitespace-pre py-2"
        >
          {lines.map((line, i) => (
            <div key={i} className={`flex ${badLines.has(i + 1) ? 'bg-red-500/10' : ''}`}>
              <span className="w-10 shrink-0 select-none pr-2 text-right text-slate-300 dark:text-slate-600">
                {i + 1}
              </span>
              <span className="pr-3 text-slate-800 dark:text-slate-100">
                {highlight(line)}
                {'\n'}
              </span>
            </div>
          ))}
        </div>
        <textarea
          ref={textareaRef}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          onScroll={syncScroll}
          rows={rows}
          spellCheck={false}
          autoCapitalize="off"
          autoComplete="off"
          // Transparent text over the mirror: the caret stays native.
          className="absolute inset-0 resize-none overflow-auto whitespace-pre bg-transparent py-2 pl-10 pr-3 text-transparent caret-slate-800 outline-none dark:caret-slate-100"
          style={{ lineHeight: '1.25rem' }}
        />
      </div>
      {issues.length > 0 && (
        <ul className="mt-2 space-y-0.5 text-xs text-red-600 dark:text-red-400">
          {issues.slice(0, 5).map((issue, i) => (
            <li key={i}>
              {issue.line > 0 ? `L${issue.line}: ` : ''}
              {issue.message}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
