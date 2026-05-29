# terraform-provider-ecspresso

A Terraform provider that manages Amazon ECS services through [kayac/ecspresso](https://github.com/kayac/ecspresso). Runs ecspresso as a Go library inside Terraform — no `null_resource + local-exec`, no three-phase apply.

**Status:** Published on the [Terraform Registry](https://registry.terraform.io/providers/fujiwara/ecspresso/latest) as `fujiwara/ecspresso`. Build / release: [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md). Design: [docs/DESIGN.md](docs/DESIGN.md).

## Quick start

`main.tf`:

```hcl
terraform {
  required_providers {
    ecspresso = { source = "fujiwara/ecspresso" }
  }
}

provider "ecspresso" {}

resource "ecspresso_service" "app" {
  config_path = "${path.module}/ecspresso.yml"

  # Must list every `tfstate(...)` reference your ecspresso config uses;
  # missing keys fail the apply. A diff here triggers `ecspresso deploy`.
  tfstate_values = {
    "aws_lb_target_group.app.arn" = aws_lb_target_group.app.arn
    "aws_iam_role.task.arn"       = aws_iam_role.task.arn
  }
}
```

Run `terraform apply`. Set AWS credentials and any `{{ env "FOO" }}` / `{{ must_env "FOO" }}` vars `ecspresso.yml` reads in the shell that runs `terraform apply`, the same way you would for `ecspresso deploy`. The provider injects an in-memory tfstate plugin backed by `tfstate_values`, so a `plugins:` block in `ecspresso.yml` is not required when the only consumer is this provider; if one is present (e.g. for shared CLI use), the provider's overrides take over and the on-disk / S3 tfstate is never read.

## `ecspresso_service` reference

### Arguments

| name | description |
|------|-------------|
| `config_path` *(required)* | Path to `ecspresso.yml`. Prefer `"${path.module}/..."`. Changing this forces replacement. |
| `tfstate_values` | Object whose keys are tfstate addresses (`"aws_iam_role.task"`, `"output.foo"`, …) and whose values are passed to ecspresso's `tfstate(...)` lookups. A diff here is the redeploy trigger. |
| `tfstate_func_prefix` | Matches a tfstate plugin's `func_prefix`. Default `""`. Only needed with multiple tfstate plugins. |
| `destroy_action` | `delete` (default) or `ignore`. `ignore` removes the resource from Terraform state without touching AWS. |

### Computed attributes

| name | description |
|------|-------------|
| `id` | `<cluster>/<service>` |
| `service_arn`, `service_name`, `cluster_arn`, `cluster_name` | ECS identifiers. |
| `last_apply_at` | RFC3339 timestamp of the last apply that actually ran `ecspresso deploy`. In `plan`, `(known after apply)` means the next apply *may* redeploy — whether it actually does depends on ecspresso's diff against AWS. |
| `ecspresso_version` | Version of the ecspresso library bundled into this provider build. A provider upgrade that ships a newer ecspresso shows up here as a plain attribute diff and never triggers a redeploy on its own. The same string is shown on the [Registry provider page](https://registry.terraform.io/providers/fujiwara/ecspresso/latest). |

Task definition identity (`arn` / `family` / `revision`) and other live AWS-side details are intentionally not exposed — see Notes for how to get them.

## Notes

### Why this exists

The typical layout — ECS services on ecspresso, surrounding resources (IAM, ALB, VPC, Application Auto Scaling, CodeDeploy) on Terraform — forces a three-phase apply: `terraform apply` → `ecspresso deploy` → `terraform apply`. The community workaround is `null_resource + local-exec`, but it can't expose service attributes and is awkward to destroy.

This provider runs ecspresso as a Go library inside Terraform, exposes service identifiers as computed attributes, and lets Terraform's dependency graph drive the ordering.

### Design philosophy

**Terraform handles bootstrap and dependency wiring. The `ecspresso` CLI handles day-to-day application deploys.** Both roles share the same `ecspresso.yml` / `taskdef.json` / `service_def.json` files, but Terraform deliberately stays out of the ongoing deploy loop.

- The **only** signal that triggers a Terraform-side redeploy is a diff in `tfstate_values` (or `tfstate_func_prefix`). When a Terraform-managed IAM Role ARN, target group ARN, etc. changes, ecspresso re-runs to pick it up.
- Terraform doesn't track `taskdef.json` / `service_def.json` — ship those via `ecspresso deploy` CLI.
- Task definition revision is not surfaced as an attribute; it advances on every CLI deploy and can't be kept authoritative.

### `config_path` resolution

Relative paths are resolved against the **working directory of the `terraform` process** (where `terraform apply` ran), not the directory of the `.tf` file. Use `"${path.module}/ecspresso.yml"` (or an absolute path) so the resource keeps working when the module is consumed from elsewhere or under `terraform -chdir=...`.

### `tfstate_values` semantics

Keys are tfstate addresses (e.g. `"aws_iam_role.task"`, `"output.foo"`), values can be any Terraform type. Nested paths like `tfstate('aws_iam_role.task.arn')` resolve through the same map.

**`tfstate_values` is the complete input set when running through this provider.** The provider hands ecspresso an in-memory tfstate backed by `tfstate_values` only — the on-disk / S3 tfstate of the *targeted* tfstate plugin is never read in provider mode. Missing keys fail the apply with `is not found in tfstate`. By design — scanned-state fallback would let Terraform-unaware changes leak into a deploy. The same `ecspresso.yml` still works from the CLI (which reads the on-disk / S3 tfstate normally because the CLI path doesn't inject anything).

The provider injects into exactly one tfstate plugin, selected by `tfstate_func_prefix` (default `""`). If `ecspresso.yml` declares **multiple** tfstate plugins, only the one whose `func_prefix` matches is fed from `tfstate_values`; the others run normally and read their own source (e.g. a shared network tfstate from S3). If `tfstate_func_prefix` matches *no* declared tfstate plugin while others are declared, the apply emits a warning — those lookups would silently read from a file instead of `tfstate_values`, the usual sign of a mis-set prefix.

"Missing keys fail" applies to **apply** (Create / Update). During a `plan` **refresh** (Read), the config is rendered against the `tfstate_values` already in state, which can legitimately lag the configuration — e.g. you reference a resource created in the same apply, or you just edited the config / `tfstate_values`. When that render can't resolve a key, the refresh is **skipped with a warning** and the last-known state is kept, so the plan still proceeds; the apply then re-renders with the planned `tfstate_values`. This is what lets a from-scratch `plan` → `apply` (where the ECS cluster does not exist yet and `cluster: tfstate('aws_ecs_cluster.main.name')` is unresolvable until apply) build in one shot.

### `last_apply_at` is a Terraform-side timestamp

`last_apply_at` is the timestamp of the host that ran `terraform apply` *when that apply actually invoked `ecspresso deploy`* (Terraform side, not AWS — use `data "aws_ecs_service"` for live AWS-side status). `(known after apply)` in `plan` means the next apply may redeploy; whether it really does depends on ecspresso's diff against AWS — if the rendered definitions already match what's deployed, deploy is skipped and the previous timestamp is preserved.

### Reading the live AWS state

Wire `data "aws_ecs_service"` against this resource. The data source is re-read on every plan, so it reflects the live AWS state regardless of how many CLI deploys have happened since the last `terraform apply`:

```hcl
data "aws_ecs_service" "app" {
  service_name = ecspresso_service.app.service_name
  cluster_arn  = ecspresso_service.app.cluster_arn
}

# data.aws_ecs_service.app.task_definition, .desired_count, .launch_type, ...
```

### Forcing a redeploy, passing CLI flags

The provider does not expose a way to force a redeploy or pass `ecspresso deploy` flags (`--force-new-deployment`, `--no-wait`, `--suspend-auto-scaling`, …) as Terraform attributes. Run the ecspresso CLI directly against the same `ecspresso.yml` when you need any of those. The provider also has no `envs` attribute — see Quick start for OS env handling.

### Adopting an existing ECS service (no `terraform import`)

`ecspresso_service` deliberately does not implement `terraform import`. The authoritative identity is the `ecspresso.yml` (plus its task / service definition templates), not the cluster/service name pair — an import identifier alone would not be enough information.

To adopt an already-deployed service:

1. Point `config_path` at the `ecspresso.yml` that already deploys the service.
2. Add the `ecspresso_service` resource to `.tf` with whatever `tfstate_values` etc. you want Terraform to manage.
3. `terraform apply`.

The first adoption-apply runs `ecspresso diff` first. When the rendered local definitions already match the deployed service / task definition, no `ecspresso deploy` is invoked — the service is left untouched and `last_apply_at` keeps its previous value (or stays empty if this is the very first apply). Otherwise it deploys, which means a new task definition revision is registered and the service is updated in place (not recreated).

## License

MIT

## Author

fujiwara <fujiwara.shunichiro@gmail.com>
