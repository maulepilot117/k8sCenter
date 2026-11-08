// Types for the JSX runtime of files whose jsxImportSource pragma is react.
// tsconfig.json resolves `react/jsx-runtime` to preact/compat's runtime, so
// those files load compat at run time, but compat ships that module without
// type declarations. Its exports are Preact's own runtime's, so it takes
// those types, JSX namespace included.
declare module "react/jsx-runtime" {
  export * from "preact/jsx-runtime";
}

declare module "react/jsx-dev-runtime" {
  export * from "preact/jsx-runtime";
}
