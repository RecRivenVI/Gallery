import eslint from '@eslint/js';
import reactHooks from 'eslint-plugin-react-hooks';
import reactRefresh from 'eslint-plugin-react-refresh';
import tseslint from 'typescript-eslint';

export default tseslint.config(
  { ignores: ['dist', 'node_modules', 'playwright-report', 'test-results', 'src/api/schema.gen.ts'] },
  eslint.configs.recommended,
  ...tseslint.configs.strictTypeChecked,
  ...tseslint.configs.stylisticTypeChecked,
  {
    files: ['**/*.{js,mjs,cjs}'],
    extends: [tseslint.configs.disableTypeChecked]
  },
  {
    files: ['**/*.{ts,tsx}'],
    languageOptions: {
      parserOptions: { projectService: true, tsconfigRootDir: import.meta.dirname }
    },
    plugins: { 'react-hooks': reactHooks, 'react-refresh': reactRefresh },
    rules: {
      ...reactHooks.configs.recommended.rules,
      '@typescript-eslint/consistent-type-imports': ['error', { prefer: 'type-imports' }],
      '@typescript-eslint/no-confusing-void-expression': 'off',
      '@typescript-eslint/no-misused-promises': ['error', { checksVoidReturn: { attributes: false } }],
      '@typescript-eslint/consistent-type-definitions': 'off',
      '@typescript-eslint/no-deprecated': 'off',
      '@typescript-eslint/restrict-template-expressions': 'off',
      '@typescript-eslint/array-type': 'off',
      '@typescript-eslint/no-unnecessary-type-assertion': 'off',
      'react-hooks/set-state-in-effect': 'off',
      'react-refresh/only-export-components': 'off'
    }
  },
  {
    files: ['vite.config.ts', 'playwright.config.ts'],
    rules: { '@typescript-eslint/no-unsafe-assignment': 'off', '@typescript-eslint/no-unsafe-call': 'off' }
  },
  {
    files: ['**/*.test.{ts,tsx}', 'tests/**/*.ts'],
    rules: { '@typescript-eslint/no-unsafe-call': 'off', '@typescript-eslint/no-empty-function': 'off' }
  }
);
