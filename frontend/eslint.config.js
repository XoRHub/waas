import tseslint from 'typescript-eslint';
import reactHooks from 'eslint-plugin-react-hooks';
import jsxA11y from 'eslint-plugin-jsx-a11y';
import prettier from 'eslint-config-prettier';

// Flat config. typescript-eslint stays on the NON-type-checked preset on
// purpose: `tsc -b` (the typecheck script) already covers typing, and
// the type-checked preset is 5-10x slower in CI. react-hooks brings the
// rule this setup exists for: exhaustive-deps.
export default tseslint.config(
  { ignores: ['dist/**', 'coverage/**'] },
  ...tseslint.configs.recommended,
  {
    files: ['src/**/*.{ts,tsx}'],
    plugins: { 'react-hooks': reactHooks, 'jsx-a11y': jsxA11y },
    rules: {
      ...reactHooks.configs.recommended.rules,
      ...jsxA11y.flatConfigs.recommended.rules,
      // Our labels nest their text two spans deep (title + hint); the
      // rule's default depth of 2 cannot see it. The controls ARE
      // nested and the text IS there.
      'jsx-a11y/label-has-associated-control': ['error', { depth: 4 }],
      // role="application" IS an interactive composite role (our
      // remote-desktop surfaces are focusable keyboard targets); the
      // rule's default whitelist only knows tabpanel.
      'jsx-a11y/no-noninteractive-tabindex': ['error', { roles: ['tabpanel', 'application'] }],
    },
  },
  prettier,
);
