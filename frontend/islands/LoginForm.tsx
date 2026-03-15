import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useAuth } from "@/lib/auth.ts";
import { Alert } from "@/components/ui/Alert.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { Input } from "@/components/ui/Input.tsx";
import { ApiError } from "@/lib/api.ts";
import {
  MAX_PASSWORD_LENGTH,
  MAX_USERNAME_LENGTH,
} from "@/lib/wizard-constants.ts";

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
      {error.value && <Alert variant="error">{error.value}</Alert>}

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
        maxLength={MAX_USERNAME_LENGTH}
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
        maxLength={MAX_PASSWORD_LENGTH}
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
