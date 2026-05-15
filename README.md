# terraform-provider-ecspresso

A Terraform provider that manages Amazon ECS services through [kayac/ecspresso](https://github.com/kayac/ecspresso).

## Status

**Early development.** Phase 2 is in place: `Create` / `Read` / `Update` / `Delete` are wired to ecspresso v2 as a Go library. `tfstate_values` is accepted by the schema but not yet injected into ecspresso (that needs a small upstream change — Phase 4 in [docs/DESIGN.md](docs/DESIGN.md)).

## Trying it locally (dev override)

Build the provider, then point Terraform at the local binary via dev overrides:

```sh
go build -o terraform-provider-ecspresso .
```

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
  config_path = "./ecspresso.yml"
}
```

`terraform init` is not required with dev overrides — just `terraform plan` / `terraform apply`. AWS credentials come from the standard environment (`AWS_PROFILE`, `AWS_REGION`, `AWS_ACCESS_KEY_ID`, etc.).

## Why

The typical layout — ECS services managed by ecspresso, surrounding resources (IAM, ALB, VPC, Application Auto Scaling, CodeDeploy) managed by Terraform — forces a three-phase apply: `terraform apply` → `ecspresso deploy` → `terraform apply`. The community workaround is `null_resource + local-exec`, which works but cannot expose attributes of the deployed service, cannot be imported, and is awkward to destroy.

This provider runs ecspresso as a Go library inside Terraform, exposes the resulting service identifiers as computed attributes, supports import, and lets Terraform's dependency graph drive the ordering directly.

## Design philosophy

**Terraform handles bootstrap and dependency wiring. `ecspresso` CLI handles day-to-day application deploys.** The two roles share the same `ecspresso.yml` / `taskdef.json` / `service_def.json` files, but Terraform deliberately stays out of the ongoing deploy loop.

Concretely:

- The **only** signals that trigger a Terraform-side redeploy are diffs to Terraform inputs the user wrote into the resource (notably `tfstate_values` and `envs`). When a Terraform-managed IAM Role ARN changes, ecspresso has to be re-run to pick it up — that is what this provider is for.
- Changes to `taskdef.json` / `service_def.json` are **not** Terraform's concern. The provider does not hash the files, does not track them, and does not redeploy when they change. Application teams update those files and ship via `ecspresso deploy` CLI without involving Terraform.
- The AWS-side task definition revision is read into the computed attributes on refresh, but is never compared against any Terraform input. A `terraform apply` after a hundred CLI deploys produces no spurious diff.

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
  config_path = "./ecspresso.yml"

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
| `config_path` | yes | Path to `ecspresso.yml`. |
| `tfstate_values` | no | Map injected into ecspresso's tfstate plugin when `from_provider: true` is set in the config. Keys are the tfstate addresses referenced from `ecspresso.yml`. A diff in this map is the primary signal that triggers a redeploy. |
| `destroy_action` | no | `delete` (default) scales the service to 0, drains tasks, then deletes. `ignore` removes the resource from Terraform state without touching AWS — useful when external dependencies (e.g. CodeDeploy deployment groups) make the destroy order tricky. |

To force a redeploy without changing any input, use `ecspresso deploy --force-new-deployment` from the CLI. `terraform apply -replace=ecspresso_service.app` also works but performs destroy+create, which causes downtime — the CLI path is the safe one.

`ecspresso deploy` flags such as `--no-wait`, `--suspend-auto-scaling`, etc. are intentionally not surfaced as Terraform attributes — pass them to the CLI when invoking ecspresso directly.

If `ecspresso.yml` references OS environment variables via `{{ env "FOO" }}` / `{{ must_env "FOO" }}`, set them in the shell that invokes `terraform apply`. The provider intentionally does not expose an `envs` attribute — those values are application-side concerns owned by the ecspresso CLI workflow, not by Terraform.

#### Computed attributes

- `id` — `<cluster>/<service>`
- `service_arn`, `service_name`
- `cluster_arn`, `cluster_name`
- `task_definition_arn`, `task_definition_family`, `task_definition_revision`

## License

MIT

## Author

fujiwara <fujiwara.shunichiro@gmail.com>
