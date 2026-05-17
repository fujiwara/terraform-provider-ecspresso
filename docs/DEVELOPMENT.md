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
`TF_ACC=1` (set automatically by `make acc-test`) and on
`ECSPRESSO_TEST_CONFIG_PATH`, so `go test ./...` on a developer machine
that has no AWS access is unaffected.

A self-contained fixture lives under [`examples/acceptance/`](../examples/acceptance/),
including a Terraform stack that creates the prerequisite AWS resources
(an empty ECS cluster, task execution role, security group in the
default VPC). Use it as-is or as a template for your own setup. The
flow:

```sh
# Bring up the AWS prerequisites.
cd examples/acceptance/bootstrap
terraform init && terraform apply
cd ..

# Run the acceptance test from the repo root.
export AWS_REGION=ap-northeast-1
export ECSPRESSO_TEST_CONFIG_PATH=$PWD/ecspresso.jsonnet
cd ../..
make acc-test

# Tear down.
cd examples/acceptance/bootstrap
terraform destroy
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
drives the same flow on GitHub-hosted runners: bootstrap
`terraform apply` → `make acc-test` → bootstrap `terraform destroy`.
The destroy step uses `if: always()` so the cluster / role / SG get
cleaned up even when the test fails.

One-time setup:

1. **AWS OIDC provider.** Register
   `token.actions.githubusercontent.com` as an IAM OIDC provider in the
   target account (audience `sts.amazonaws.com`).
2. **IAM role.** Create a role the workflow can assume via OIDC. Trust
   policy template (substitute `<account-id>` and the repo path):

   ```json
   {
     "Version": "2012-10-17",
     "Statement": [{
       "Effect": "Allow",
       "Principal": {
         "Federated": "arn:aws:iam::<account-id>:oidc-provider/token.actions.githubusercontent.com"
       },
       "Action": "sts:AssumeRoleWithWebIdentity",
       "Condition": {
         "StringEquals": {
           "token.actions.githubusercontent.com:aud": "sts.amazonaws.com"
         },
         "StringLike": {
           "token.actions.githubusercontent.com:sub": "repo:fujiwara/terraform-provider-ecspresso:environment:acc-test"
         }
       }
     }]
   }
   ```

   The role needs ECS / IAM (`PassRole` + CRUD on the role this stack
   creates) / EC2 (VPC + security group lookup and CRUD) permissions
   plus the managed-policy attach action to wire
   `AmazonECSTaskExecutionRolePolicy` onto the task role. Scoping the
   policy to the `ecspresso-provider-acc-test` cluster / role / SG
   names is feasible.
3. **`acc-test` environment.** On the GitHub repository, *Settings →
   Environments → `acc-test`*. Under **Environment variables**, add
   `AWS_ROLE_ARN` set to the role ARN above. The ARN is not a secret
   (assume succeeds only via the OIDC trust relationship), so a
   variable is enough — no environment secrets are required.

After that, go to *Actions → acceptance test → Run workflow* on
`main`, optionally override the region input, and the run boots the
bootstrap stack, runs the test, and tears everything down. With
`desiredCount: 0` in the service definition no Fargate tasks are
launched, so the AWS-side cost of a run is effectively zero.

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
