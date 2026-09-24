import js from '@eslint/js';
import reactHooks from 'eslint-plugin-react-hooks';
import tseslint from 'typescript-eslint';

export default tseslint.config(
  {
    ignores: ['dist', 'coverage', 'node_modules'],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ['**/*.{ts,tsx}'],
    plugins: {
      'react-hooks': reactHooks,
    },
    // v7 added the React Compiler rules to recommended. Existing code does not
    // satisfy them yet, so they warn until it does; v5's two rules stay as
    // they were.
    rules: {
      ...Object.fromEntries(
        Object.keys(reactHooks.configs.recommended.rules).map((rule) => [rule, 'warn']),
      ),
      'react-hooks/rules-of-hooks': 'error',
      'react-hooks/exhaustive-deps': 'warn',
    },
  },
  {
    // New in eslint 10's recommended set; warns until existing code is cleaned up.
    rules: {
      'no-useless-assignment': 'warn',
    },
  },
);
