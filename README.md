# pb-infra

ProfanityBandits Infrastructure - a infrastructure repository for the website profanitybandits.net and its associated services.

## Technologies used

- Kubernetes & Helm
- Traefik

## Flux workloads

Apps are wired in the root **`kustomization.yaml`**. **Vintage Story** was **removed** from sync (chart registry `tccr.io` unreachable from the cluster). Local notes live in **`docs/vintagestory-removed.md`** (that `docs/` tree is gitignored until you publish it).
