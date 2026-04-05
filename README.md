# pb-infra

ProfanityBandits Infrastructure - a infrastructure repository for the website profanitybandits.net and its associated services.

## Technologies used

- Kubernetes & Helm
- Traefik

## Flux workloads

Apps are wired in the root **`kustomization.yaml`**. **Vintage Story** was **removed** from sync (chart registry `tccr.io` unreachable from the cluster). Local notes live in **`docs/vintagestory-removed.md`** (that `docs/` tree is gitignored until you publish it).

### GitOps workflow

**The cluster should follow what is on the remote Git branch Flux tracks** (e.g. `main` after **push**). Changes apply through Flux controllers and their intervals—not by running `flux reconcile` or other imperative Flux commands as part of day-to-day work.

Use **`kubectl`**, **`flux get`**, **`kubectl describe`**, **logs**, etc. **only to inspect and debug**. Reserve **`flux reconcile`** / suspend / resume for rare operational situations; do not treat them as the normal “deploy” step after editing this repo.
