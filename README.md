# terraform-provider-ecspresso

A Terraform provider that manages Amazon ECS services through [kayac/ecspresso](https://github.com/kayac/ecspresso).

## Status

**Pre-release.** `Create` / `Read` / `Update` / `Delete` are wired to ecspresso v2 as a Go library. `tfstate_values` is fed into ecspresso's tfstate plugin via the override mechanism in [tfstate-lookup v1.12.0](https://github.com/fujiwara/tfstate-lookup/releases/tag/v1.12.0) and the plugin instance registry in ecspresso v2.8.4 (the `optional: true` tfstate flag depended on by the first-apply path is from the post-v2.8.4 commit currently pinned via Go pseudo-version). Release artifacts and the goreleaser configuration are aligned with the Terraform Registry publishing requirements — see [docs/DESIGN.md](docs/DESIGN.md) for the full plan and the "Releasing" section below for the publishing checklist.

## Trying it locally (dev override)

Build the provider, then point Terraform at the local binary via dev overrides:

```sh
make build
```

`make build` passes `-tags no_gcs,no_azurerm` to `go build`, mirroring the
[ecspresso CLI build](https://github.com/kayac/ecspresso/blob/v2/Makefile).
These tags drop the GCS and AzureRM `tfstate-lookup` backends, which ECS users
effectively never use as their Terraform state backend, and shave roughly 30 MB
off the binary. The S3 and Terraform Cloud / Terraform Enterprise backends stay
enabled. The release builds in `.goreleaser.yml` apply the same tags.

Add to `~/.terraformrc`:

```hcl
provider_installation {
  dev_overrides {
    "fujiwara/ecspresso" = "/home/fujiwara/src/github.com/fujiwara/terraform-provider-ecspresso"
  }
  direct {}
}
```

In your Terraform working directory, write a minimal `main.tf`:

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
}
```

`terraform init` is not required with dev overrides — just `terraform plan` / `terraform apply`. AWS credentials come from the standard environment (`AWS_PROFILE`, `AWS_REGION`, `AWS_ACCESS_KEY_ID`, etc.).

## Why

The typical layout — ECS services managed by ecspresso, surrounding resources (IAM, ALB, VPC, Application Auto Scaling, CodeDeploy) managed by Terraform — forces a three-phase apply: `terraform apply` → `ecspresso deploy` → `terraform apply`. The community workaround is `null_resource + local-exec`, which works but cannot expose attributes of the deployed service, cannot be imported, and is awkward to destroy.

This provider runs ecspresso as a Go library inside Terraform, exposes the resulting service identifiers as computed attributes, supports import, and lets Terraform's dependency graph drive the ordering directly.

## Design philosophy

**Terraform handles bootstrap and dependency wiring. `ecspresso` CLI handles day-to-day application deploys.** The two roles share the same `ecspresso.yml` / `taskdef.json` / `service_def.json` files, but Terraform deliberately stays out of the ongoing deploy loop.

Concretely:

- The **only** signal that triggers a Terraform-side redeploy is a diff in `tfstate_values`. When a Terraform-managed IAM Role ARN, target group ARN, etc. changes, ecspresso has to be re-run to pick it up — that is what this provider is for.
- Changes to `taskdef.json` / `service_def.json` are **not** Terraform's concern. The provider does not hash the files, does not track them, and does not redeploy when they change. Application teams update those files and ship via `ecspresso deploy` CLI without involving Terraform.
- The AWS-side task definition revision is deliberately not surfaced as an attribute. It advances on every CLI deploy and Terraform cannot keep it authoritative, so exposing it would only invite stale references and spurious diffs.

## Planned usage

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

Caveat: the same `ecspresso.yml`, when run from the ecspresso CLI directly, will also accept `optional: true` and silently fall back to an empty tfstate if `path` / `url` is mistyped — `tfstate(...)` lookups then surface as "not found" errors rather than as a clear 404 on the configured URL. If a config file is shared between this provider and direct CLI use, either accept that tradeoff or keep the CLI-side config free of `optional: true`.

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

## Releasing

The release pipeline (`.github/workflows/tagpr-release.yml`) drives [Songmu/tagpr](https://github.com/Songmu/tagpr) for tagging and [goreleaser](https://goreleaser.com/) for building, signing, and publishing artifacts in the layout required by the Terraform Registry:

- Per-OS/arch zip archives: `terraform-provider-ecspresso_v{VERSION}_{OS}_{ARCH}.zip`
- `terraform-provider-ecspresso_v{VERSION}_SHA256SUMS` and a detached `.sig` (GPG)
- `terraform-provider-ecspresso_v{VERSION}_manifest.json` (sourced from `terraform-registry-manifest.json`)

One-time setup before the first release:

1. **GPG signing key.** Generate a key pair (no passphrase or with one — both supported), export the **public** key as ASCII-armored, and register that public key under your Terraform Registry account. The fingerprint must match what goreleaser will use to sign.
2. **GitHub Secrets.** On the GitHub repository, add:
   - `GPG_PRIVATE_KEY` — the ASCII-armored **private** key, exported with `gpg --armor --export-secret-keys $FINGERPRINT`.
   - `PASSPHRASE` — passphrase for the key (set even if empty, to satisfy the action input).
3. **Terraform Registry.** Sign in at [registry.terraform.io](https://registry.terraform.io/) with the GitHub account that owns this repository, click *Publish → Provider*, pick the repo, and confirm the namespace (`fujiwara/ecspresso`).

After that, releases happen by merging a tagpr-generated PR into `main`; the workflow tags, builds signed artifacts, and publishes a GitHub Release. The Terraform Registry polls GitHub for new tags and picks the artifacts up automatically.

## License

MIT

## Author

fujiwara <fujiwara.shunichiro@gmail.com>
