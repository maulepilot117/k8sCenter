// deno-lint-ignore-file react-no-danger
import { define } from "@/utils.ts";

export default define.page(function App({ Component }) {
  return (
    <html lang="en" class="h-full" style="background-color:#05080f">
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
        {
          /* Apply saved animation prefs before render to prevent flash.
            SYNC: the hardcoded #05080f background (html/body attributes
            below) must match bgBase in shared/themes/liquid-glass.json
            and the styles.css fallbacks. */
        }
        <script
          dangerouslySetInnerHTML={{
            __html:
              `(function(){try{var t=localStorage.getItem("kc.theme");if(t==="light")document.documentElement.classList.add("theme-light")}catch(e){}})();(function(){try{var a=localStorage.getItem("k8scenter-animations");if(a==="false")document.documentElement.classList.add("no-animations")}catch(e){}})()`,
          }}
        />
      </head>
      <body class="h-full" style="background-color:#05080f">
        <Component />
      </body>
    </html>
  );
});
