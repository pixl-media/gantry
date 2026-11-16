# gantry web UI

React + Vite + MUI, served by Caddy in its own container; talks only to the
agent's API. General rules, the project map and the security essentials are
in the root `AGENTS.md`.

## Commands

pnpm, Node 24+; CI and the image use Node 26 LTS. Run from the repo root:

- `make web` – Vite dev server on 127.0.0.1:5173, `/api` forwarded to the agent
- `make web-test` – Vitest; `make web-lint` – ESLint, Prettier, typecheck
- `make web-generate` – regenerate `web/src/api/schema.gen.ts` from the spec
  (CI fails if it's out of date)
- `make web-build`, `make web-image`

## Conventions

- API access only through the generated types (`web/src/api/schema.gen.ts`,
  never edited by hand) and the hooks in `web/src/api/queries.ts`.
- **Every UI text goes through i18n** (`t('…')`) and exists in both
  `web/src/i18n/locales/en.json` and `de.json`; a test checks that the keys
  match, and the typecheck rejects unknown keys.
- MUI components and the theme in `web/src/theme.ts`; no external fonts or
  scripts and no inline scripts (the CSP in `web/Caddyfile` allows only the
  own origin).
- Action rules (what's possible when) live in `web/src/lib/actions.ts` and
  mirror the agent's rules.
- The app uses a data router (`createBrowserRouter` in `main.tsx`, routes in
  `App.tsx`), which `useBlocker` needs; tests for such components render with
  `renderWithDataRouter`. The CodeMirror editor (`components/CodeEditor.tsx`)
  is loaded lazily; tests replace it with `test/codeEditorStub.tsx`.
- Prettier style: single quotes, trailing commas.

## Security

The root `AGENTS.md` has the rules for both parts. In the web UI:

- **Never store tokens in the browser** (`localStorage`, `sessionStorage`,
  JS-readable cookies); only the `HttpOnly` session cookie. Mutations whose
  variables hold the token or `.env` values use `gcTime: 0`.
- `.env` values are shown only after an explicit click, through the reveal
  endpoint.
