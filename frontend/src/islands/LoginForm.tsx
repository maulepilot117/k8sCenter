import { type FormEvent, useState } from "react";
import { Alert } from "@/components/ui/Alert.tsx";
import { Button } from "@/components/ui/Button.tsx";
import { Input } from "@/components/ui/Input.tsx";
import { ApiError } from "@/lib/api.ts";
import { useAuth } from "@/lib/auth.ts";
import {
  MAX_PASSWORD_LENGTH,
  MAX_USERNAME_LENGTH,
} from "@/lib/wizard-constants.ts";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

export default function LoginForm() {
  const { login } = useAuth();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (!IS_BROWSER) return;

    setError("");
    setLoading(true);

    try {
      await login(username, password);
      globalThis.location.href = "/";
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.status === 401) {
          setError("Invalid username or password");
        } else if (err.status === 429) {
          setError("Too many attempts. Please wait a minute.");
        } else {
          setError(err.detail ?? "Login failed");
        }
      } else {
        setError("Unable to connect to the server");
      }
    } finally {
      setLoading(false);
    }
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-5">
      {error && <Alert variant="error">{error}</Alert>}

      <Input
        label="Username"
        type="text"
        value={username}
        onChange={(e) => setUsername(e.currentTarget.value)}
        required
        autoComplete="username"
        autoFocus
        maxLength={MAX_USERNAME_LENGTH}
      />

      <Input
        label="Password"
        type="password"
        value={password}
        onChange={(e) => setPassword(e.currentTarget.value)}
        required
        autoComplete="current-password"
        maxLength={MAX_PASSWORD_LENGTH}
      />

      <Button
        type="submit"
        variant="primary"
        size="lg"
        loading={loading}
        className="w-full"
      >
        Sign in
      </Button>
    </form>
  );
}
