# OIDC bootstrap for GitHub Actions acceptance test

Provisions the AWS prerequisites for [`/.github/workflows/acc-test.yml`](../../../.github/workflows/acc-test.yml):

- The GitHub Actions OIDC provider (`token.actions.githubusercontent.com`).
- An IAM role the workflow assumes when running in `environment: acc-test`.
- A narrowly-scoped inline policy on the role: only ECS service and
  task definition lifecycle against the `ecspresso-provider-acc-test`
  cluster, `iam:PassRole` for the pre-provisioned task execution role,
  EC2 read-only describes, and `s3:GetObject` on the bootstrap
  tfstate. **No `iam:CreateRole`, `ecs:CreateCluster`, or
  `ec2:CreateSecurityGroup`** — those are deliberately out of scope so
  the role cannot escalate privileges or create new AWS resources.

This is a one-time setup; once applied, the role keeps working for
every acceptance test run.

## Prerequisite

The bootstrap stack (`../bootstrap/`) is applied first and its state
lives in S3 (see `bootstrap/README.md` for the `-backend-config`
flags). This stack needs to know the same bucket and key so it can
grant the role `s3:GetObject` on the tfstate object.

## Run

```sh
cd examples/acceptance/oidc
terraform init
terraform apply \
  -var tfstate_bucket=<your-tfstate-bucket> \
  -var tfstate_key=terraform-provider-ecspresso/acceptance/bootstrap.tfstate
```

Then copy the `role_arn` output and set it on the GitHub repository:

- *Settings → Environments → `acc-test` → Environment variables*:
  - `AWS_ROLE_ARN` = the `role_arn` output above
  - `TFSTATE_URL`  = `s3://<your-tfstate-bucket>/terraform-provider-ecspresso/acceptance/bootstrap.tfstate`

Neither value is a secret (the role ARN is just an identifier; the S3
URL only resolves with a successful OIDC assume), so variables — not
secrets — are the right choice.

## Variables

| variable | default | purpose |
|---|---|---|
| `github_repo` | `fujiwara/terraform-provider-ecspresso` | Repo slug. Set to your fork if you cloned to a different namespace. |
| `environment_name` | `acc-test` | GitHub Environment allowed to assume the role. Match the workflow's `environment:` value. |
| `tfstate_bucket` | (required) | S3 bucket holding the bootstrap stack's tfstate object. |
| `tfstate_key` | `terraform-provider-ecspresso/acceptance/bootstrap.tfstate` | S3 object key for the bootstrap stack's tfstate. |

## If the OIDC provider already exists

AWS allows only one OIDC provider per URL per account, so if
`token.actions.githubusercontent.com` is already registered (e.g. for
another repository), `terraform apply` will fail. Two ways out:

1. Import the existing provider into this state:
   ```sh
   terraform import aws_iam_openid_connect_provider.github \
     arn:aws:iam::<account-id>:oidc-provider/token.actions.githubusercontent.com
   ```
2. Or replace the `resource "aws_iam_openid_connect_provider" "github"`
   block with a `data "aws_iam_openid_connect_provider" "github"` and
   reference its `arn` in the role's assume policy.

## Tear down

```sh
terraform destroy
```
