# Rubrik operator guide

k8sCenter can show Rubrik Security Cloud (RSC) protection for the namespaces of each cluster it manages. With writes turned on, it can also take on-demand snapshots, change SLA domains and restore snapshots. Velero stays as it is; Rubrik is a second backup provider beside it.

The provider is a **preview**. It is off by default and every part of it stays off until you turn it on.

## What you need

- k8sCenter with PostgreSQL. The connection, the cluster mappings, the write operations and the poller's history are stored there. Without PostgreSQL the backend logs an error at startup and the provider stays off.
- An RSC tenant (`https://<tenant>.my.rubrik.com`) that already protects your Kubernetes clusters. k8sCenter reads RSC; it does not install or configure the Rubrik agent.
- An RSC service account for k8sCenter (see below).
- Network egress from the k8sCenter backend pod to the tenant on port 443.

## 1. Create the RSC service account

In RSC, create a service account for k8sCenter and give it a custom role. The service account's role is the ceiling for everything done through k8sCenter: k8sCenter narrows it per user, and never widens it.

- Limit the role to Kubernetes objects. k8sCenter only reads and acts on Kubernetes protection, so it needs no access to other workload types.
- For monitoring only, a view-only role on Kubernetes objects and SLA domains is enough.
- For writes, add on-demand snapshot, SLA assignment and restore (recovery) on Kubernetes objects. Leave out anything you never want a k8sCenter user to do.

Keep the client ID and client secret RSC shows you. The secret is shown once.

k8sCenter exchanges the client ID and secret for a short-lived token at the tenant's `/api/client_token` endpoint, and sends GraphQL to `/api/graphql`.

## 2. Turn the provider on

In your Helm values:

```yaml
rubrik:
  enabled: true        # routes and the poller
  writesEnabled: false # keep writes off until step 7
```

The chart sets `KUBECENTER_RUBRIK_ENABLED` (and `KUBECENTER_RUBRIK_WRITESENABLED` when `writesEnabled` is also true) only when the value is true. Both values must be booleans; the chart's schema refuses a quoted `"false"`.

**Restart the backend after changing either value.** The backend reads these settings once, at startup, and `helm upgrade` does not restart it when only the config changes. Until it restarts, the old setting stays in force, so turning writes off does not take effect:

```bash
helm upgrade <release> ./helm/kubecenter -n <namespace> -f values.yaml
kubectl rollout restart deployment/<fullname>-backend -n <namespace>
kubectl rollout status deployment/<fullname>-backend -n <namespace>
```

`<fullname>` is the release name, or `<release>-kubecenter` when the release name does not contain `kubecenter` (`kubectl get deploy -n <namespace>` shows it). Then check `writesEnabled` in `GET /api/v1/rubrik/status` (step 4).

While `rubrik.enabled` is false, no `/api/v1/rubrik` route exists (every path is a 404) and no call reaches RSC. With `writesEnabled` false, every snapshot, SLA change and restore is refused, for admins too. Saving the connection and the cluster mappings (steps 4 and 5) still works.

## 3. Allow egress to the tenant

The backend calls the tenant over HTTPS on port 443.

- **Default NetworkPolicy.** When `networkPolicy.egress.kubernetesApiCIDRs` is empty, the backend's egress rule already allows port 443 to any address except link-local and metadata ranges, so the tenant is reachable with no change.
- **Restricted API CIDRs.** When you set `kubernetesApiCIDRs`, port 443 is open only to those CIDRs. Add the tenant's addresses to `networkPolicy.egress.extraAllowedCIDRs`. RSC addresses can change; check Rubrik's published egress ranges for your region.
- **Egress proxy.** The client honors `HTTPS_PROXY` from the backend's environment. The chart has no value for it, so set it on the backend Deployment yourself (for example with `kubectl set env`), and check it is still set after each chart upgrade. Then allow egress to the proxy. `extraAllowedCIDRs` opens only ports 80, 443 and 5432, so a proxy listening on another port (3128 or 8080, say) needs a NetworkPolicy you add outside the chart. The tenant URL is still checked against the public-address guard before every request, but the backend resolves the tenant host itself and cannot see the address the proxy connects to.

The tenant URL must be `https`, must resolve to a public address, and is checked again at every request. Redirects are never followed.

## 4. Connect k8sCenter to RSC

An admin saves the connection on **Settings → Rubrik** (`/settings/rubrik`), or through the API below. Every API request needs an admin's access token and the CSRF header.

### Pre-filling the tenant URL from Helm

The chart can supply the tenant URL, so an admin does not have to type it:

```yaml
rubrik:
  enabled: true
  tenantUrl: https://<tenant>.my.rubrik.com
```

This is only a suggestion. The saved connection is still the one source of truth.

- While no connection is saved, `GET /api/v1/rubrik/status` gives admins the value as `connection.suggestedTenantUrl`. Nothing is stored and nothing is audited until an admin enters the client ID and secret and saves. The save is checked, probed and audited like any other.
- Once a connection is saved, the saved URL wins. Changing or removing `tenantUrl` afterwards has no effect; change the URL on the settings page (or with `PUT /rubrik/connection`) instead. Moving to another tenant then needs the client secret again and clears the cluster mappings, as below.
- Helm supplies the URL only. The client ID and secret are always entered by an admin.
- The value must be `https://` and a host, with no path, query, fragment or credentials. The chart's schema refuses anything else. A value that still fails the backend's check (for example one set without the chart) is dropped at startup with a warning that does not include it.
- Like the other Rubrik values, the backend reads it at startup, so restart the backend after changing it (step 2).

```bash
API=https://k8scenter.example.com/api/v1
AUTH=(-H "Authorization: Bearer $TOKEN" -H "X-Requested-With: XMLHttpRequest" -H "Content-Type: application/json")

# Save the connection. k8sCenter probes the tenant with these credentials before saving.
curl "${AUTH[@]}" -X PUT "$API/rubrik/connection" \
  -d '{"tenantUrl":"https://<tenant>.my.rubrik.com","clientId":"<client id>","clientSecret":"<client secret>"}'

# Check it. The answer carries the connection revision and its health.
curl "${AUTH[@]}" "$API/rubrik/status"
```

The client secret is stored encrypted with AES-256-GCM. The key is `KUBECENTER_DATABASE_ENCRYPTIONKEY`, or the JWT secret when that is unset. The Helm chart does not set the encryption key, so under the chart the key is always the JWT secret: after rotating the JWT secret, save the connection again with `clientSecret`. The client secret is never returned, logged or written to the audit log; status only reports `hasSecret`. To change the client ID without retyping the secret, leave `clientSecret` out. That is refused if the tenant URL moves to another origin: a stored secret is never sent to a different tenant. Moving the connection to another tenant clears every cluster mapping (`mappingsCleared` in the answer).

Connection `health` is one of `ok`, `auth_failed`, `unreachable`, `failing`, `secret_required`, `unknown` (configured, not contacted yet), `unavailable` or `not_configured`. Failures are reported by class only; RSC's own error text is never shown or stored.

## 5. Map each cluster

Each k8sCenter cluster is read from one RSC Kubernetes cluster that an admin maps to it. Nothing is matched by name.

```bash
# RSC's Kubernetes clusters, each with the k8sCenter cluster already mapped to it.
curl "${AUTH[@]}" "$API/rubrik/rsc-clusters"

# Map the local cluster (id "local"). connectionRevision is the revision from status;
# a save made after the connection changed is refused.
curl "${AUTH[@]}" -X PUT "$API/rubrik/mappings/local" \
  -d '{"rscClusterId":"<RSC cluster id>","connectionRevision":<revision>}'

# List or remove mappings.
curl "${AUTH[@]}" "$API/rubrik/mappings"
curl "${AUTH[@]}" -X DELETE "$API/rubrik/mappings/local"
```

Map a remote cluster by its k8sCenter cluster id. On a remote cluster, only admins see Rubrik data, through the same admin gate as every other remote selection.

Once a cluster is mapped, the poller checks it every 5 minutes. It records failed jobs and sends them to Notification Center. After an outage it reads back at most 24 hours and says so in a gap notice. The poller keeps its events for 30 days and finished write operations for 90 days.

## 6. Grant users access

Admins see and do everything Rubrik allows (writes still need `writesEnabled`). Every other user needs Kubernetes RBAC grants on virtual resources in the `rubrik.kubecenter.io` API group. k8sCenter checks them with a SelfSubjectAccessReview as the user, per namespace. The group and resource names are a contract: renaming them would break your Roles.

| Grant | Verb and resource | Lets the user |
|---|---|---|
| Read | `get` `protections` | see the namespace's protection, snapshots, jobs and Rubrik notifications |
| Snapshot | `create` `snapshots` | take an on-demand snapshot |
| SLA change | `update` `slaassignments` | assign or change the SLA domain |
| Restore | `create` `restores` | restore a snapshot into the namespace |

The write grants do not stand alone. A snapshot or SLA change also needs the Read grant in that namespace. A restore needs the Read grant in the source namespace and the Restore grant in the namespace it writes into. A source whose namespace no longer exists is admin-only.

Read only, for a team's namespace:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: rubrik-read
  namespace: team-a
rules:
  - apiGroups: ["rubrik.kubecenter.io"]
    resources: ["protections"]
    verbs: ["get"]
```

Read plus each write grant:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: rubrik-operator
  namespace: team-a
rules:
  - apiGroups: ["rubrik.kubecenter.io"]
    resources: ["protections"]
    verbs: ["get"]
  - apiGroups: ["rubrik.kubecenter.io"]
    resources: ["snapshots"]
    verbs: ["create"]
  - apiGroups: ["rubrik.kubecenter.io"]
    resources: ["slaassignments"]
    verbs: ["update"]
  - apiGroups: ["rubrik.kubecenter.io"]
    resources: ["restores"]
    verbs: ["create"]
```

Bind each Role with a RoleBinding to the user or group, as for any other Role.

### Restores need the user's own permissions too

Rubrik's agent restores as its own service account, so k8sCenter checks that the restore stays within what the user could create by hand. In every namespace a restore writes into, the user's own identity (admins included) must be able to:

- `create` roles, rolebindings, serviceaccounts, secrets, configmaps, services, persistentvolumeclaims, deployments, statefulsets and daemonsets;
- `bind` and `escalate` roles and clusterroles.

A restore into a different namespace also needs `get secrets` in the source namespace, because the snapshot carries its Secrets. Restores of multi-namespace or cluster-scoped sources are admin-only. A restore is refused when k8sCenter cannot name every namespace it would write into.

## 7. Before enabling writes

Rules you never wrote for Rubrik can grant its verbs. Kubernetes matches a rule's API group and resource separately, so a rule grants a Rubrik verb when:

- its `apiGroups` holds `"*"` or `rubrik.kubecenter.io`, and
- its `resources` holds `"*"` or the Rubrik resource name (`protections`, `snapshots`, `slaassignments` or `restores`), and
- its `verbs` holds `"*"` or the verb.

So `cluster-admin` grants them, and so does a rule written for another backup tool with `apiGroups: ["*"]` and `resources: ["restores"]` or `["snapshots"]`. Find those roles and their bindings before you set `writesEnabled: true`:

```bash
# Roles and ClusterRoles with a rule that can grant a Rubrik write verb.
FILTER='any(.rules[]?;
  ((.apiGroups // []) | any(. == "*" or . == "rubrik.kubecenter.io"))
  and ((.resources // []) | any(. == "*" or . == "snapshots" or . == "slaassignments" or . == "restores"))
  and ((.verbs // []) | any(. == "*" or . == "create" or . == "update")))'
kubectl get clusterroles -o json | jq -r ".items[] | select($FILTER) | \"ClusterRole/\(.metadata.name)\""
kubectl get roles -A -o json | jq -r ".items[] | select($FILTER) | \"Role/\(.metadata.namespace)/\(.metadata.name)\""

# Who is bound to one of them. Set KIND to Role or ClusterRole and NAME to its name.
KIND=ClusterRole NAME="cluster-admin"
kubectl get clusterrolebindings,rolebindings -A -o json | jq -r --arg kind "$KIND" --arg name "$NAME" '.items[]
  | select(.roleRef.kind == $kind and .roleRef.name == $name)
  | "\(.metadata.namespace // "-")/\(.metadata.name): \([.subjects[]? | .kind + ":" + .name] | join(", "))"'
```

For a Role, the bindings that matter are the RoleBindings in the Role's own namespace.

RBAC objects are not the whole answer. k8sCenter asks the API server with a SelfSubjectAccessReview as the user, so anything that authorizes the user also grants the Rubrik verbs. That includes `system:masters` and a cloud provider's IAM authorizer (GKE, EKS, AKS), and neither shows up in the queries above. Spot-check the users and groups who should not have write access:

```bash
kubectl auth can-i create restores.rubrik.kubecenter.io -n <namespace> --as <user> --as-group <group>
kubectl auth can-i create snapshots.rubrik.kubecenter.io -n <namespace> --as <user> --as-group <group>
kubectl auth can-i update slaassignments.rubrik.kubecenter.io -n <namespace> --as <user> --as-group <group>
```

Narrow or accept each grant, then turn writes on (and restart the backend, as in step 2):

```yaml
rubrik:
  enabled: true
  writesEnabled: true
```

Every write is audited once, with the user and the outcome. The tenant URL and the client secret never appear in the audit log. Writes are rate-limited per user.

## Preview limits

- The web pages for Rubrik (settings, namespace views, write dialogs and the protection overview) ship in later releases. Until then, setup is through the API above, and Rubrik failures appear in Notification Center.
- Remote clusters are admin-only.
