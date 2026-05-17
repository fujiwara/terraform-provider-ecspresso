# Development

Build, dev override, and release notes for `terraform-provider-ecspresso`.
See [DESIGN.md](DESIGN.md) for the design rationale and the resource model.

## Status

**Pre-release.** `Create` / `Read` / `Update` / `Delete` are wired to
ecspresso v2 as a Go library. `tfstate_values` is fed into ecspresso's
tfstate plugin via the override mechanism in
[tfstate-lookup v1.12.0](https://github.com/fujiwara/tfstate-lookup/releases/tag/v1.12.0)
and the plugin instance registry in ecspresso v2.8.4. The `optional: true`
tfstate flag the first-apply path depends on is from a post-v2.8.4 commit
on ecspresso's `v2` branch, currently pinned via Go pseudo-version. The
release artifacts and `.goreleaser.yml` are aligned with the Terraform
Registry publishing requirements; see [Releasing](#releasing) below.

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
`TF_ACC=1` (set automatically by `make acc-test`) and a per-fixture env
var, so `go test ./...` on a developer machine that has no AWS access is
unaffected.

```sh
export AWS_REGION=us-east-1                          # or AWS_DEFAULT_REGION
export ECSPRESSO_TEST_CONFIG_PATH=/abs/path/to/ecspresso.yml
make acc-test
```

`ECSPRESSO_TEST_CONFIG_PATH` is an absolute path to an `ecspresso.yml`
that already points at a deployable service (i.e. the same kind of
config you'd pass to `config_path` on the resource). The test runs the
full Create / Read / Delete cycle, so the cluster and task / service
definitions referenced from the config must be valid and the credentials
in the shell must be allowed to register task definitions, create the
service, and delete it again. Skipped silently when the env var is unset.

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
