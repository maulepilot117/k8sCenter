/** Standard API response envelope from the Go backend. */
export interface APIResponse<T> {
  data: T;
  metadata?: {
    total?: number;
    page?: number;
    pageSize?: number;
    continue?: string;
  };
}

/** Standard API error response from the Go backend. */
export interface APIError {
  error: {
    code: number;
    message: string;
    detail?: string;
  };
}

/** User info from /auth/me — matches backend auth.User struct. */
export interface UserInfo {
  id: string;
  username: string;
  provider: string;
  kubernetesUsername: string;
  kubernetesGroups: string[];
  roles: string[];
}
