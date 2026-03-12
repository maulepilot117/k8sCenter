import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useAuth } from "@/lib/auth.ts";
import { Button } from "@/components/ui/Button.tsx";
import { Input } from "@/components/ui/Input.tsx";
import { ApiError } from "@/lib/api.ts";

export default function LoginForm() {
  const { login } = useAuth();
  const username = useSignal("");
  const password = useSignal("");
  const error = useSignal("");
  const loading = useSignal(false);

  async function handleSubmit(e: Event) {
    e.preventDefault();
    if (!IS_BROWSER) return;

    error.value = "";
    loading.value = true;

    try {
      await login(username.value, password.value);
      globalThis.location.href = "/";
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.status === 401) {
          error.value = "Invalid username or password";
        } else if (err.status === 429) {
          error.value = "Too many attempts. Please wait a minute.";
        } else {
          error.value = err.detail ?? "Login failed";
        }
      } else {
        error.value = "Unable to connect to the server";
      }
    } finally {
      loading.value = false;
    }
  }

  return (
    <form onSubmit={handleSubmit} class="space-y-5">
      {error.value && (
        <div class="rounded-md bg-red-50 px-4 py-3 text-sm text-red-800 dark:bg-red-900/30 dark:text-red-400">
          {error.value}
        </div>
      )}

      <Input
        label="Username"
        type="text"
        value={username.value}
        onInput={(e) => {
          username.value = (e.target as HTMLInputElement).value;
        }}
        required
        autocomplete="username"
        autofocus
      />

      <Input
        label="Password"
        type="password"
        value={password.value}
        onInput={(e) => {
          password.value = (e.target as HTMLInputElement).value;
        }}
        required
        autocomplete="current-password"
      />

      <Button
        type="submit"
        variant="primary"
        size="lg"
        loading={loading.value}
        class="w-full"
      >
        Sign in
      </Button>
    </form>
  );
}
