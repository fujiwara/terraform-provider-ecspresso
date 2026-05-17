# OIDC bootstrap for GitHub Actions acceptance test

Provisions the AWS prerequisites for [`/.github/workflows/acc-test.yml`](../../../.github/workflows/acc-test.yml):

- The GitHub Actions OIDC provider (`token.actions.githubusercontent.com`).
- An IAM role the workflow assumes when running in `environment: acc-test`.
- An inline policy on the role with the ECS / IAM / EC2 permissions the
  bootstrap stack and acceptance test need.

This is a one-time setup; once applied, the role keeps working for every
acceptance test run.

## Run

```sh
cd examples/acceptance/oidc
terraform init
terraform apply
```

Then copy the `role_arn` output and set it as `AWS_ROLE_ARN` under
*Settings → Environments → `acc-test` → Environment variables* on the
GitHub repository. (It's an ARN, not a secret — a variable is enough.)

## Variables

| variable | default | purpose |
|---|---|---|
| `github_repo` | `fujiwara/terraform-provider-ecspresso` | Repo slug. Set to your fork if you cloned to a different namespace. |
| `environment_name` | `acc-test` | GitHub Environment allowed to assume the role. Match the workflow's `environment:` value. |

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

Do this **after** `terraform destroy` on the bootstrap stack, so the
role still has permission to clean those resources up.
