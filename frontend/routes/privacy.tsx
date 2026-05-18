import { define } from "@/utils.ts";

const LAST_UPDATED = "2026-05-18";

export default define.page(function PrivacyPage() {
  return (
    <div class="mx-auto max-w-3xl px-6 py-12 text-text-primary">
      <header class="mb-8">
        <h1 class="text-3xl font-bold">Privacy Policy</h1>
        <p class="mt-2 text-sm text-text-secondary">
          Last updated: {LAST_UPDATED}
        </p>
      </header>

      <section class="mb-8 space-y-3">
        <h2 class="text-xl font-semibold">What k8sCenter is</h2>
        <p>
          k8sCenter is a self-hosted Kubernetes management platform. The web and
          mobile apps are clients for a k8sCenter server that you (or your
          organization) operate. We do not run a multi-tenant cloud service; we
          do not store your data on our servers.
        </p>
        <p>
          Kubernetes cluster credentials live on the k8sCenter server you
          deploy, encrypted with AES-256-GCM. The mobile app never stores
          cluster credentials on the device — it only stores its own session JWT
          after you sign in to your k8sCenter instance, and a Firebase Cloud
          Messaging device token for push notifications.
        </p>
      </section>

      <section class="mb-8 space-y-3">
        <h2 class="text-xl font-semibold">What we collect</h2>
        <p>
          The mobile and web apps collect a minimal set of data, scoped to
          delivering core functionality:
        </p>
        <ul class="list-disc space-y-2 pl-6">
          <li>
            <strong>Device identifier (FCM token)</strong>{" "}
            — used to deliver push notifications about cluster alerts. Linked to
            your account after sign-in. Not used for tracking. Invalidated when
            you sign out.
          </li>
          <li>
            <strong>
              Crash and performance data (Sentry) — opt-in, off by default
            </strong>{" "}
            — when you enable crash reporting in Settings, stack traces and
            limited diagnostic context are sent to Sentry. Not linked to your
            account. Not used for tracking. Kubernetes resource names, tokens,
            and request bodies are scrubbed on the device before any event
            leaves.
          </li>
        </ul>
        <p>
          That is everything. We do not collect contact info, location, health
          data, financial data, contacts, photos, messages, browsing history,
          search history, advertising identifiers, or any other personal data.
          We do not embed analytics or advertising SDKs.
        </p>
      </section>

      <section class="mb-8 space-y-3">
        <h2 class="text-xl font-semibold">Third-party services</h2>
        <ul class="list-disc space-y-2 pl-6">
          <li>
            <strong>Firebase Cloud Messaging</strong>{" "}
            (Google) — receives your device token and the message payload sent
            by your k8sCenter server. Required for push notifications; disabled
            when your k8sCenter operator turns off the FCM feature.
          </li>
          <li>
            <strong>Sentry</strong>{" "}
            — receives crash and performance data only when you toggle Crash
            Reporting on. PII is scrubbed on the device before transmission;
            events arriving in Sentry contain obfuscated stack frames, generic
            exception messages, and no user identifiers.
          </li>
        </ul>
      </section>

      <section class="mb-8 space-y-3">
        <h2 class="text-xl font-semibold">Your rights</h2>
        <ul class="list-disc space-y-2 pl-6">
          <li>
            <strong>Opt out of crash reporting</strong>{" "}
            at any time via Settings → Crash Reporting. Effective immediately;
            pending events on the device are dropped.
          </li>
          <li>
            <strong>Delete your data</strong>{" "}
            by signing out and uninstalling the app. The session JWT lives only
            in iOS Keychain / Android Keystore and is removed on uninstall. The
            FCM device token is invalidated on the k8sCenter server at sign-out.
          </li>
          <li>
            <strong>Access your data</strong>{" "}
            via the k8sCenter server you (or your organization) operate.
            Server-side audit logs contain the record of your actions; reach out
            to the operator for access.
          </li>
        </ul>
      </section>

      <section class="mb-8 space-y-3">
        <h2 class="text-xl font-semibold">Children</h2>
        <p>
          k8sCenter is a developer tool. We do not knowingly collect data from
          children under 13. The App Store age rating is 4+ because the app
          contains no objectionable content; it is not aimed at children.
        </p>
      </section>

      <section class="mb-8 space-y-3">
        <h2 class="text-xl font-semibold">Changes</h2>
        <p>
          When this policy changes materially, we update the date at the top and
          note the change in the project release notes. Major changes — new data
          categories, new third-party services — will be surfaced in the app's
          release notes.
        </p>
      </section>

      <section class="mb-8 space-y-3">
        <h2 class="text-xl font-semibold">Contact</h2>
        <p>
          Open a confidential issue at{" "}
          <a
            href="https://github.com/maulepilot117/k8sCenter/issues"
            class="text-brand underline hover:no-underline"
            rel="noopener noreferrer"
            target="_blank"
          >
            github.com/maulepilot117/k8sCenter/issues
          </a>{" "}
          for privacy concerns. The full data-handling specification for the
          mobile app — including the on-device PII scrubber's exact behavior —
          is documented in{" "}
          <code class="rounded bg-surface px-1 py-0.5 text-sm">
            mobile/docs/APP_PRIVACY.md
          </code>{" "}
          in the project repository.
        </p>
      </section>
    </div>
  );
});
