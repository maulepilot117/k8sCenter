// deno-lint-ignore-file react-no-danger
import { define } from "@/utils.ts";

export default define.page(function App({ Component }) {
  return (
    <html lang="en" class="h-full">
      <head>
        <meta charset="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1.0" />
        <title>k8sCenter</title>
        <meta name="color-scheme" content="light dark" />
        {/* Apply saved theme before render to prevent flash of wrong theme */}
        <script
          dangerouslySetInnerHTML={{
            __html:
              `(function(){var t=localStorage.getItem("theme");if(t==="dark"||(t!=="light"&&matchMedia("(prefers-color-scheme:dark)").matches))document.documentElement.classList.add("dark")})()`,
          }}
        />
      </head>
      <body class="h-full bg-slate-50 text-slate-900 dark:bg-slate-900 dark:text-slate-100">
        <Component />
      </body>
    </html>
  );
});
