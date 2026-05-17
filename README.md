# terraform-provider-ecspresso

A Terraform provider that manages Amazon ECS services through [kayac/ecspresso](https://github.com/kayac/ecspresso). Runs ecspresso as a Go library inside Terraform — no `null_resource + local-exec`, no three-phase apply.

**Status:** Published on the [Terraform Registry](https://registry.terraform.io/providers/fujiwara/ecspresso/latest) — `terraform init` pulls it from `source = "fujiwara/ecspresso"`. See [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) for local build / release notes and [docs/DESIGN.md](docs/DESIGN.md) for the design rationale.

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

`ecspresso.yml` needs a `tfstate` plugin. Add `optional: true` for the **first** `terraform apply`, then remove it:

```yaml
plugins:
  - name: tfstate
    config:
      url: s3://my-bucket/path/to/terraform.tfstate
      optional: true   # bootstrap only — delete after the first apply (see Notes)
```

Run `terraform apply`. AWS credentials and any `{{ env "FOO" }}` / `{{ must_env "FOO" }}` values that `ecspresso.yml` reads come from the shell that runs `terraform apply` — the same way you'd set them before running `ecspresso deploy` directly.

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
| `last_apply_at` | RFC3339 timestamp of the last apply that ran deploy. In `plan`, `(known after apply)` means the next apply will redeploy. |

Task definition identity (`arn` / `family` / `revision`) and other live AWS-side details are intentionally not exposed — see Notes for how to get them.

## Notes

These are the details behind the quick start. Skim what's relevant.

### Why this exists

The typical layout — ECS services managed by ecspresso, surrounding resources (IAM, ALB, VPC, Application Auto Scaling, CodeDeploy) managed by Terraform — forces a three-phase apply: `terraform apply` → `ecspresso deploy` → `terraform apply`. The community workaround is `null_resource + local-exec`, which works but cannot expose attributes of the deployed service and is awkward to destroy. This provider runs ecspresso as a Go library inside Terraform, exposes service identifiers as computed attributes, and lets Terraform's dependency graph drive ordering directly.

### Design philosophy

**Terraform handles bootstrap and dependency wiring. The `ecspresso` CLI handles day-to-day application deploys.** Both roles share the same `ecspresso.yml` / `taskdef.json` / `service_def.json` files, but Terraform deliberately stays out of the ongoing deploy loop.

- The **only** signal that triggers a Terraform-side redeploy is a diff in `tfstate_values` (or `tfstate_func_prefix`). When a Terraform-managed IAM Role ARN, target group ARN, etc. changes, ecspresso is re-run to pick it up.
- Changes to `taskdef.json` / `service_def.json` are **not** Terraform's concern. The provider does not hash the files, does not track them, and does not redeploy when they change. Ship those via `ecspresso deploy` CLI.
- The AWS-side task definition revision is deliberately not surfaced as an attribute (advances on every CLI deploy → Terraform cannot keep it authoritative → stale references and spurious diffs).

### `config_path` resolution

Relative paths are resolved against the **working directory of the `terraform` process** (where `terraform apply` ran), not the directory of the `.tf` file. Use `"${path.module}/ecspresso.yml"` (or an absolute path) so the resource keeps working when the module is consumed from elsewhere or under `terraform -chdir=...`.

### `tfstate_values` semantics

Each key is a tfstate address at the resource level (e.g. `"aws_iam_role.task"`, `"output.foo"`), and the value can be any Terraform type — a whole resource attribute map, a list, a bool, or a scalar. The corresponding `tfstate(...)` lookups in ecspresso's jsonnet/template (including nested ones like `tfstate('aws_iam_role.task.arn')`) are resolved against this map.

**`tfstate_values` is the complete input set when running through this provider.** On every apply, the provider discards the tfstate plugin's scanned data and serves lookups from `tfstate_values` only. The same `ecspresso.yml` keeps working from the CLI (it still reads the on-disk / S3 tfstate), but from inside Terraform, any `tfstate(...)` reference not covered by `tfstate_values` surfaces as `is not found in tfstate` at apply time. This is the intended early signal — falling back to the scanned state would let Terraform-unaware changes leak into a deploy.

### `optional: true` is bootstrap-only

The Terraform backend has not yet written the state object on the first apply, so without `optional: true` the tfstate plugin's initial load fails with 404 / file not found before the provider can push `tfstate_values`. With `optional: true` ecspresso logs a warning and continues with an empty state; the overrides take over.

**Remove the flag after the first successful apply.** Once the backend has written the state object, the flag has no useful effect, and leaving it on weakens the CLI side: an `optional: true` config silently falls back to empty on a 404, so a typo in `path` / `url` surfaces as confusing "not found" errors from `tfstate(...)` lookups instead of as a clear failure on the configured URL.

### `last_apply_at` is a Terraform-side timestamp

`last_apply_at` is the time on the host that ran `terraform apply`, not the AWS-side deployment time — use `data "aws_ecs_service"` for live AWS-side deployment status. Its purpose is plan visibility: `(known after apply)` means the next apply will redeploy; an unchanged value means the apply will only update Terraform state (e.g. when only `destroy_action` changed).

### Reading the live AWS state

Wire `data "aws_ecs_service"` against this resource. The data source is re-read on every plan, so it reflects the live AWS state regardless of how many CLI deploys have happened since the last `terraform apply`:

```hcl
data "aws_ecs_service" "app" {
  service_name = ecspresso_service.app.service_name
  cluster_arn  = ecspresso_service.app.cluster_arn
}

# data.aws_ecs_service.app.task_definition, .desired_count, .launch_type, ...
```

The reference creates an implicit dependency, so `depends_on` is not required.

### Forcing a redeploy, passing CLI flags

The provider does not expose a way to force a redeploy or pass `ecspresso deploy` flags (`--force-new-deployment`, `--no-wait`, `--suspend-auto-scaling`, …) as Terraform attributes. When you need any of those, run the ecspresso CLI directly against the same `ecspresso.yml`.

The provider also does not expose an `envs` attribute. `{{ env "FOO" }}` / `{{ must_env "FOO" }}` are read from the OS environment of the `terraform` process — see Quick start for the basic case.

### Adopting an existing ECS service (no `terraform import`)

`ecspresso_service` deliberately does not implement `terraform import`. The authoritative identity is the `ecspresso.yml` (plus its task / service definition templates), not the cluster/service name pair — an import identifier alone would not be enough information.

To adopt an already-deployed service:

1. Point `config_path` at the `ecspresso.yml` that already deploys the service.
2. Add the `ecspresso_service` resource to `.tf` with whatever `tfstate_values` etc. you want Terraform to manage.
3. `terraform apply`.

`ecspresso deploy` is idempotent against an existing service — it only registers a new task definition revision / updates the service if there is a real diff. So the worst case for the first adoption-apply is the same outcome as running `ecspresso deploy` on the CLI: a no-op or the deploy that would have happened anyway. The service is never recreated from scratch. (For a strict "no deploy on first apply", render the ecspresso config so its diff against AWS is empty beforehand.)

## License

MIT

## Author

fujiwara <fujiwara.shunichiro@gmail.com>
