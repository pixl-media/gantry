import type { CodeEditorProps } from '../components/CodeEditor';

/** Stands in for the CodeMirror editor in tests: a plain textarea. */
export default function CodeEditorStub({
  value,
  onChange,
  readOnly,
  ariaLabel,
}: CodeEditorProps) {
  return (
    <textarea
      aria-label={ariaLabel}
      value={value}
      readOnly={readOnly}
      onChange={(e) => onChange?.(e.target.value)}
    />
  );
}
