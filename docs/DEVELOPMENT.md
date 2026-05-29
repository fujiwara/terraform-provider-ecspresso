# Development

Build, dev override, and release notes for `terraform-provider-ecspresso`.
See [DESIGN.md](DESIGN.md) for the design rationale and the resource model.

## Status

**Pre-release.** `Create` / `Read` / `Update` / `Delete` are wired to
ecspresso as a Go library. `tfstate_values` is turned into an in-memory
`*tfstate.TFState` (via [tfstate-lookup](https://github.com/fujiwara/tfstate-lookup)'s
`Empty()` + `SetOverrides`) and handed to ecspresso through the
`WithPluginInstance` AppOption ([kayac/ecspresso#1031](https://github.com/kayac/ecspresso/pull/1031)).
The injected instance bypasses the tfstate plugin's Setup, so no on-disk
/ S3 tfstate is read, a `plugins:` block is optional, and even
config-level `tfstate(...)` fields resolve from `tfstate_values` — which
is why the earlier `optional: true` first-apply workaround is no longer
needed. `WithPluginInstance` lives on ecspresso's `pre-v3` branch,
currently pinned via Go pseudo-version. The release artifacts and
`.goreleaser.yml` are aligned with the Terraform Registry publishing
requirements; see [Releasing](#releasing) below.

## Building locally (dev override)

Build the provider, then point Terraform at the local binary via dev
overrides:

```sh
make build
```

`make build` passes `-tags no_gcs,no_azurerm` to `go build`, mirroring the
[ecspresso CLI build](https://github.com/kayac/ecspresso/blob/v2/Makefile).
These tags drop the GCS and AzureRM `tfstate-lookup` backends, which ECS
users effectively never use as their Terraform state backend, and shave
roughly 30 MB off the binary. The S3 and Terraform Cloud / Terraform
Enterprise backends stay enabled. The release builds in `.goreleaser.yml`
apply the same tags.

Add to `~/.terraformrc`:

```hcl
provider_installation {
  dev_overrides {
    "fujiwara/ecspresso" = "/path/to/terraform-provider-ecspresso"
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

`terraform init` is not required with dev overrides — just `terraform plan`
/ `terraform apply`. AWS credentials come from the standard environment
(`AWS_PROFILE`, `AWS_REGION`, `AWS_ACCESS_KEY_ID`, etc.).

## Tests

Unit tests (no AWS access required):

```sh
make test
```

Acceptance tests hit real AWS via a real ECS service. They are gated on
`TF_ACC=1` (set automatically by `make acc-test`) and on
`ECSPRESSO_TEST_CONFIG_PATH`, so `go test ./...` on a developer machine
that has no AWS access is unaffected.

A self-contained fixture lives under [`examples/acceptance/`](../examples/acceptance/).
It provisions the prerequisite AWS resources (empty ECS cluster, task
execution role, security group in the default VPC) — all free at rest
— and is designed to be **applied once and left in place** so every
acceptance test run only touches the ECS service. State goes to S3 so
the GitHub Actions workflow can read it without re-applying.

```sh
# One-time: apply the bootstrap stack.
cd examples/acceptance/bootstrap
terraform init \
  -backend-config="bucket=<your-tfstate-bucket>" \
  -backend-config="key=terraform-provider-ecspresso/acceptance/bootstrap.tfstate" \
  -backend-config="region=ap-northeast-1"
terraform apply

# Run the acceptance test from the repo root.
cd ../../..
export AWS_REGION=ap-northeast-1
export TFSTATE_URL=s3://<your-tfstate-bucket>/terraform-provider-ecspresso/acceptance/bootstrap.tfstate
export ECSPRESSO_TEST_CONFIG_PATH=$PWD/examples/acceptance/ecspresso.jsonnet
make acc-test
```

See [`examples/acceptance/README.md`](../examples/acceptance/README.md)
for the full description of the fixture and what it provisions.

If you point `ECSPRESSO_TEST_CONFIG_PATH` at a different ecspresso config
of your own, just make sure the cluster, task definition, and service
definition it references are valid and the credentials in the shell are
allowed to register task definitions, create the service, and delete it.

### Running acceptance tests in GitHub Actions

A `workflow_dispatch`-only workflow at
[.github/workflows/acc-test.yml](../.github/workflows/acc-test.yml)
runs `make acc-test` on a GitHub-hosted runner. The bootstrap stack
is assumed to be already applied (above), so the workflow only needs
the permission to create / delete the ECS service.

One-time setup:

1. **AWS OIDC provider + IAM role.** Apply the Terraform stack under
   [`examples/acceptance/oidc/`](../examples/acceptance/oidc/) **after**
   the bootstrap stack. It creates the GitHub Actions OIDC provider
   and an IAM role with a narrowly-scoped policy (ECS service +
   task definition lifecycle on the acceptance test cluster, plus
   `iam:PassRole` for the pre-provisioned task execution role and
   `s3:GetObject` on the bootstrap tfstate — no create-role / create-
   cluster / create-security-group). See that directory's README for
   the "OIDC provider already exists" fallback.

   ```sh
   cd examples/acceptance/oidc
   terraform init
   terraform apply \
     -var tfstate_bucket=<your-tfstate-bucket> \
     -var tfstate_key=terraform-provider-ecspresso/acceptance/bootstrap.tfstate
   ```

2. **`acc-test` environment.** On the GitHub repository, *Settings →
   Environments → `acc-test`*. Under **Environment variables**, add:

   - `AWS_ROLE_ARN` — the `role_arn` output from step 1.
   - `TFSTATE_URL` — `s3://<your-tfstate-bucket>/terraform-provider-ecspresso/acceptance/bootstrap.tfstate`.

   Neither value is a secret (the ARN is just an identifier; the
   bucket URL only resolves with a successful OIDC assume), so
   variables — not secrets — are the right choice.

After that, go to *Actions → acceptance test → Run workflow* on
`main`, optionally override the region, and the run will create the
ECS service via the provider, assert its computed attributes, and
delete it again. With `desiredCount: 0` in the service definition no
Fargate tasks are launched, so the AWS-side cost of a run is
effectively zero.

## Releasing

The release pipeline (`.github/workflows/tagpr-release.yml`) drives
[Songmu/tagpr](https://github.com/Songmu/tagpr) for tagging and
[goreleaser](https://goreleaser.com/) for building, signing, and
publishing artifacts in the layout required by the Terraform Registry:

- Per-OS/arch zip archives: `terraform-provider-ecspresso_v{VERSION}_{OS}_{ARCH}.zip`
- `terraform-provider-ecspresso_v{VERSION}_SHA256SUMS` and a detached `.sig` (GPG)
- `terraform-provider-ecspresso_v{VERSION}_manifest.json` (sourced from `terraform-registry-manifest.json`)

One-time setup before the first release:

1. **GPG signing key.** Generate a passphrase-less key pair, export the
   **public** key as ASCII-armored, and register that public key under your
   Terraform Registry account. The fingerprint must match the
   `GPG_FINGERPRINT` env value in `.github/workflows/tagpr-release.yml` —
   update the workflow if you re-key.
2. **`deploy` environment secret.** On the GitHub repository, under
   *Settings → Environments → `deploy` → Environment secrets*, add
   `GPG_PRIVATE_KEY` — the ASCII-armored **private** key, exported with
   `gpg --armor --export-secret-keys $FINGERPRINT`. The release job runs in
   `environment: deploy` so this secret is the only one needed for signing.
3. **Terraform Registry.** Sign in at
   [registry.terraform.io](https://registry.terraform.io/) with the GitHub
   account that owns this repository, click *Publish → Provider*, pick the
   repo, and confirm the namespace (`fujiwara/ecspresso`).

After that, releases happen by merging a tagpr-generated PR into `main`;
the workflow tags, builds signed artifacts, and publishes a GitHub Release.
The Terraform Registry polls GitHub for new tags and picks the artifacts
up automatically.
