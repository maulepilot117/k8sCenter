# Concepts

Shared domain vocabulary for this project — entities, named processes, and status
concepts with project-specific meaning. Seeded with core domain vocabulary, then accretes
as ce-compound and ce-compound-refresh process learnings; direct edits are fine. Glossary
only, not a spec or catch-all.

## Wizards

### Wizard
A guided, multi-step form that collects operator intent and turns it into a Kubernetes
manifest, rather than asking the operator to write YAML. A Wizard ends at a Review step
where the generated manifest is shown before anything reaches the cluster. Most Wizards
cover a single resource kind; a few cover a namespaced and cluster-scoped pair of kinds,
choosing between them from the scope they were opened at.

A Wizard never assembles the manifest it submits in the browser. It posts the collected
form state to the backend, which owns the translation into YAML, so the manifest an
operator reviews is produced by the same code path that would produce it anywhere else.
Some Wizards additionally show a rough client-built sketch on earlier steps; that sketch
is replaced by the server's manifest on reaching Review and is never what gets applied.
Earlier steps can be revisited, and the manifest is regenerated on arrival at Review, so
it reflects the form state as last submitted rather than a cached earlier one.

### Wizard Preview
The manifest a Wizard generates from its collected form state, rendered server-side and
shown to the operator before apply. It is the Wizard's output and its last checkpoint.

A Preview is editable: the operator may change the YAML by hand before applying, and the
edited text is what gets applied — the form state does not overwrite it afterwards. This
makes the Preview the escape hatch for anything the form cannot express. Generation is a
request that can fail or still be in flight, and the Review step offers no apply action in
either case: a Preview that has not both completed and succeeded cannot be applied.
