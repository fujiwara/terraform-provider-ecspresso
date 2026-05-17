# terraform-provider-ecspresso

A Terraform provider that manages Amazon ECS services through [kayac/ecspresso](https://github.com/kayac/ecspresso).

**Status:** Pre-release. This is not yet published to the Terraform Registry; for now consume it via `dev_overrides` — see [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md). Build, release, and contributor notes also live in that document; the design rationale lives in [docs/DESIGN.md](docs/DESIGN.md).

## Why

The typical layout — ECS services managed by ecspresso, surrounding resources (IAM, ALB, VPC, Application Auto Scaling, CodeDeploy) managed by Terraform — forces a three-phase apply: `terraform apply` → `ecspresso deploy` → `terraform apply`. The community workaround is `null_resource + local-exec`, which works but cannot expose attributes of the deployed service, cannot be imported, and is awkward to destroy.

This provider runs ecspresso as a Go library inside Terraform, exposes the resulting service identifiers as computed attributes, supports import, and lets Terraform's dependency graph drive the ordering directly.

## Design philosophy

**Terraform handles bootstrap and dependency wiring. `ecspresso` CLI handles day-to-day application deploys.** The two roles share the same `ecspresso.yml` / `taskdef.json` / `service_def.json` files, but Terraform deliberately stays out of the ongoing deploy loop.

Concretely:

- The **only** signal that triggers a Terraform-side redeploy is a diff in `tfstate_values`. When a Terraform-managed IAM Role ARN, target group ARN, etc. changes, ecspresso has to be re-run to pick it up — that is what this provider is for.
- Changes to `taskdef.json` / `service_def.json` are **not** Terraform's concern. The provider does not hash the files, does not track them, and does not redeploy when they change. Application teams update those files and ship via `ecspresso deploy` CLI without involving Terraform.
- The AWS-side task definition revision is deliberately not surfaced as an attribute. It advances on every CLI deploy and Terraform cannot keep it authoritative, so exposing it would only invite stale references and spurious diffs.

## Usage

```hcl
terraform {
  required_providers {
    ecspresso = {
      source = "fujiwara/ecspresso"
    }
  }
}

provider "ecspresso" {}

resource "ecspresso_service" "app" {
  config_path = "${path.module}/ecspresso.yml"

  # A diff in any of these values causes Terraform to re-run ecspresso deploy.
  # File contents of taskdef.json / service_def.json are intentionally NOT tracked.
  tfstate_values = {
    "aws_lb_target_group.app.arn" = aws_lb_target_group.app.arn
    "aws_iam_role.task.arn"       = aws_iam_role.task.arn
  }

  destroy_action = "delete"
}

resource "aws_appautoscaling_target" "app" {
  resource_id = "service/${ecspresso_service.app.cluster_name}/${ecspresso_service.app.service_name}"
  # ...
}
```

## Resources

### `ecspresso_service`

Runs `ecspresso deploy` against the configured ECS service.

#### Arguments

| name | required | description |
|------|----------|-------------|
| `config_path` | yes | Path to `ecspresso.yml`. Relative paths are resolved against the working directory of the `terraform` process (where `terraform apply` is invoked), **not** the directory containing the `.tf` file. Prefer `"${path.module}/ecspresso.yml"` (or an absolute path) so the resource keeps working when the module is consumed from elsewhere or when `terraform -chdir=...` is used. Changing this forces a new resource. |
| `tfstate_values` | no | Object whose keys are tfstate addresses at the resource level (e.g. `"aws_iam_role.task"`, `"output.foo"`). Each value may be any Terraform type — a whole resource attribute map, a list, a bool, or a scalar — and the corresponding `tfstate(...)` lookups in ecspresso's jsonnet/template (including nested ones like `tfstate('aws_iam_role.task.arn')`) are resolved against it. Overrides take precedence over the tfstate file the plugin loads from `path` / `url`, so this resolves the "state file is one apply behind" problem. A diff here is the primary signal that triggers a redeploy. |
| `tfstate_func_prefix` | no | Identifies which tfstate plugin in `ecspresso.yml` receives the `tfstate_values` overrides, matched against the plugin's `func_prefix`. Defaults to `""` (the no-prefix / single-plugin case). Only needed when the ecspresso config declares multiple tfstate plugins. |
| `destroy_action` | no | `delete` (default) scales the service to 0, drains tasks, then deletes. `ignore` removes the resource from Terraform state without touching AWS — useful when external dependencies (e.g. CodeDeploy deployment groups) make the destroy order tricky. |

To force a redeploy without changing any input, use `ecspresso deploy --force-new-deployment` from the CLI. `terraform apply -replace=ecspresso_service.app` also works but performs destroy+create, which causes downtime — the CLI path is the safe one.

`ecspresso deploy` flags such as `--no-wait`, `--suspend-auto-scaling`, etc. are intentionally not surfaced as Terraform attributes — pass them to the CLI when invoking ecspresso directly.

If `ecspresso.yml` references OS environment variables via `{{ env "FOO" }}` / `{{ must_env "FOO" }}`, set them in the shell that invokes `terraform apply`. The provider intentionally does not expose an `envs` attribute — those values are application-side concerns owned by the ecspresso CLI workflow, not by Terraform.

#### `ecspresso.yml` setup

The `ecspresso.yml` referenced by `config_path` should declare a `tfstate` plugin so the provider can push `tfstate_values` overrides into it. Set `optional: true` on that plugin:

```yaml
plugins:
  - name: tfstate
    config:
      url: s3://my-bucket/path/to/terraform.tfstate
      optional: true
```

`optional: true` is what lets the **first** `terraform apply` succeed. The Terraform backend has not yet written the state object the plugin is configured to read, so without this flag the plugin's initial load fails (404 / file not found) before the provider gets a chance to push overrides. With `optional: true`, ecspresso logs a warning and continues with an empty state, and the `tfstate_values` overrides take over from there. On subsequent applies the backend-written tfstate is read normally and `tfstate_values` is layered on top of it.

`optional: true` is a **bootstrap-only** flag. The recommended workflow is to share a single `ecspresso.yml` between this provider and the ecspresso CLI, add `optional: true` for the first `terraform apply`, and then remove it once the Terraform backend has written the state object. After that the tfstate `path` / `url` is always readable, so the flag has no useful effect — and keeping it around weakens the CLI side: when the same config is run from the ecspresso CLI directly, an `optional: true` config silently falls back to an empty tfstate on a 404 / file not found, so a typo in `path` / `url` surfaces as confusing "not found" errors from `tfstate(...)` lookups instead of as a clear failure on the configured URL. Removing it once it has served its bootstrap purpose is the safer default.

#### Computed attributes

- `id` — `<cluster>/<service>`
- `service_arn`, `service_name`
- `cluster_arn`, `cluster_name`
- `last_apply_at` — RFC3339 timestamp of the most recent `terraform apply` that invoked `ecspresso deploy` for this resource. This is a Terraform-side timestamp (taken on the host running `terraform apply`), **not** the AWS-side deployment time — use `data "aws_ecs_service"` for live AWS-side deployment status. Its purpose is to make `terraform plan` show whether the next apply will redeploy: `(known after apply)` means `ecspresso deploy` will run; an unchanged timestamp means the apply will only update Terraform state (e.g. when only `destroy_action` changed).

Task-definition identity (`arn` / `family` / `revision`) and other AWS-managed details (desired count, launch type, …) are intentionally not exposed as attributes of this resource. They advance on every `ecspresso deploy` — including CLI deploys that Terraform is unaware of — so any value Terraform recorded would be stale almost immediately.

When you need them inside Terraform, wire up `data "aws_ecs_service"` against this resource. The data source is re-read on every plan, so it reflects the live AWS state regardless of how many CLI deploys have happened since the last `terraform apply`:

```hcl
data "aws_ecs_service" "app" {
  service_name = ecspresso_service.app.service_name
  cluster_arn  = ecspresso_service.app.cluster_arn
}

# data.aws_ecs_service.app.task_definition, .desired_count, .launch_type, ...
```

The reference to `ecspresso_service.app` already creates an implicit dependency, so an explicit `depends_on` is not required — the data source will run after the ecspresso deploy.

#### Adopting an existing ECS service (no `terraform import`)

`ecspresso_service` deliberately does **not** implement `terraform import`. The
authoritative identity of the resource is the `ecspresso.yml` (plus its task
and service definition templates), not the cluster/service name pair, so an
identifier passed to `terraform import` would not be enough information to
reconstruct the rest of the resource — `config_path`, `tfstate_values`,
`tfstate_func_prefix`, and `destroy_action` still have to be written in `.tf`
either way.

Adopting an already-deployed service into Terraform is instead a normal
`terraform apply`:

1. Point `config_path` at the `ecspresso.yml` that already deploys the
   service in question.
2. Add the `ecspresso_service` resource to `.tf` with whatever
   `tfstate_values` etc. you want Terraform to manage.
3. `terraform apply`.

`ecspresso deploy` is idempotent against an existing service — it diffs the
rendered task and service definitions against AWS and only registers a new
task definition revision / updates the service if there is a real change. So
the worst case for the first adoption-apply is the same outcome as running
`ecspresso deploy` on the CLI: either a no-op, or the deploy that would
have happened anyway. The service is never recreated from scratch.

If you want a strict "import only, no deploy" first apply, render the
ecspresso config so that its diff against AWS comes out empty before
running `terraform apply`.

(Reminder: the first-apply success itself depends on `optional: true` on the
tfstate plugin — see "ecspresso.yml setup" above.)

## License

MIT

## Author

fujiwara <fujiwara.shunichiro@gmail.com>
