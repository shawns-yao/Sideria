import vue from "eslint-plugin-vue";
import ts from "typescript-eslint";
export default [
  ...ts.configs.recommended,
  ...vue.configs["flat/essential"],
  {
    files: ["**/*.vue"],
    languageOptions: { parserOptions: { parser: ts.parser } },
  },
  { rules: { "@typescript-eslint/no-explicit-any": "off" } },
];
