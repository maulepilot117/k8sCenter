import { createDefine } from "fresh";

export interface State {
  /** Authenticated user info, set by auth middleware. Null if not logged in. */
  user: {
    username: string;
    role: string;
  } | null;
  /** Current page title for the document head. */
  title: string;
}

export const define = createDefine<State>();
