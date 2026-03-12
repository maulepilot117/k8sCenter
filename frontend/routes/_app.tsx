import { define } from "@/utils.ts";

export default define.page(function App({ Component }) {
  return (
    <html lang="en" class="h-full">
      <head>
        <meta charset="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1.0" />
        <title>KubeCenter</title>
        <meta name="color-scheme" content="light dark" />
      </head>
      <body class="h-full bg-slate-50 text-slate-900 dark:bg-slate-900 dark:text-slate-100">
        <Component />
      </body>
    </html>
  );
});
