/** Local user record from /api/v1/users — matches backend auth.UserRecord (json:"-" on password). */
export interface LocalUser {
  id: string;
  username: string;
  k8sUsername: string;
  k8sGroups: string[];
  roles: string[];
}
