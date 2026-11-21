import js from '@eslint/js';
import { defineConfig } from 'eslint/config';
import reactHooks from 'eslint-plugin-react-hooks';
import globals from 'globals';
import tseslint from 'typescript-eslint';

export default defineConfig(
  { ignores: ['dist', 'src/api/schema.gen.ts'] },
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
    ],
    languageOptions: { globals: globals.browser },
    rules: {
      // MUI's color props take palette names (textSecondary, error); a
      // theme path such as 'text.secondary' only works in sx and is
      // silently ignored as a prop.
      'no-restricted-syntax': [
        'error',
        {
          selector: "JSXAttribute[name.name='color'] Literal[value=/\\./]",
          message:
            "Use a palette name (textSecondary, error, …) for color props; theme paths like 'text.secondary' only work in sx.",
        },
      ],
    },
  },
);
