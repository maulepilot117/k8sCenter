// deno-lint-ignore-file react-no-danger
import { define } from "@/utils.ts";

export default define.page(function App({ Component }) {
  return (
    <html lang="en" class="h-full" style="background:#0b0e14">
      <head>
        <meta charset="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1.0" />
        <title>k8sCenter</title>
        <meta name="color-scheme" content="dark" />
        {/* Geist font */}
        <link rel="preconnect" href="https://fonts.googleapis.com" />
        <link
          rel="preconnect"
          href="https://fonts.gstatic.com"
          crossOrigin="anonymous"
        />
        <link
          href="https://fonts.googleapis.com/css2?family=Geist:wght@400;500;600;700&family=Geist+Mono:wght@400;500&display=swap"
          rel="stylesheet"
        />
        {/* Apply saved theme + animation prefs before render to prevent flash */}
        <script
          dangerouslySetInnerHTML={{
            __html:
              `(function(){var themes=["nexus","dracula","tokyo-night","catppuccin","nord","one-dark","gruvbox"];var t=localStorage.getItem("k8scenter-theme");if(t&&themes.indexOf(t)!==-1)document.documentElement.dataset.theme=t;var a=localStorage.getItem("k8scenter-animations");if(a==="false")document.documentElement.classList.add("no-animations")})()`,
          }}
        />
      </head>
      <body class="h-full" style="background:#0b0e14">
        <Component />
      </body>
    </html>
  );
});
