import { define } from "@/utils.ts";
import UserWizard from "@/islands/UserWizard.tsx";

export default define.page(function NewUserPage() {
  return <UserWizard />;
});
