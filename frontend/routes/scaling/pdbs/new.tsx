import { define } from "@/utils.ts";
import PDBWizard from "@/islands/PDBWizard.tsx";

export default define.page(function NewPDBPage() {
  return (
    <PDBWizard
      onClose={() => {
        globalThis.location.href = "/scaling/pdbs";
      }}
    />
  );
});
