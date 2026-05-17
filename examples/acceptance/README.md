# Acceptance test fixture

Self-contained fixture for running `make acc-test` against real AWS.

The acceptance test in `internal/provider/service_resource_acc_test.go`
self-skips unless `ECSPRESSO_TEST_CONFIG_PATH` is set. This directory
provides that path plus a small Terraform stack that creates the
prerequisite AWS resources (an empty ECS cluster, task execution role,
security group) inside the default VPC.

## Files

- `bootstrap/main.tf` — Terraform stack for the AWS prerequisites.
  Exports `cluster_name`, `task_execution_role_arn`, `subnet_ids`,
  `security_group_id` for ecspresso to consume.
- `ecspresso.jsonnet` — ecspresso config. Reads `bootstrap/terraform.tfstate`
  via the `tfstate` plugin to resolve the bootstrap outputs.
- `taskdef.jsonnet` — Minimal Fargate task definition running one
  `public.ecr.aws/nginx/nginx:latest` container.
- `service_def.jsonnet` — Service definition; one task on Fargate in the
  first default-VPC subnet.
- `oidc/main.tf` — Optional. Provisions the GitHub Actions OIDC
  provider and the IAM role used by the GitHub Actions acceptance test
  workflow. See `oidc/README.md`. Only needed if you intend to run the
  test through `.github/workflows/acc-test.yml`.

## Run

```sh
# 1. Bring up the AWS prerequisites.
cd examples/acceptance/bootstrap
terraform init
terraform apply
cd ..

# 2. From the repo root, run the acceptance test.
export AWS_REGION=ap-northeast-1                       # or your region
export ECSPRESSO_TEST_CONFIG_PATH=$PWD/ecspresso.jsonnet
cd ../..
make acc-test

# 3. Tear down.
cd examples/acceptance/bootstrap
terraform destroy
```

The acceptance test creates the ECS service `ecspresso-provider-acc-test`
in the bootstrap cluster, asserts the resource's computed attributes
(`id`, `service_arn`, `service_name`, `cluster_arn`, `cluster_name`,
`last_apply_at`), and removes the service again. Only the cluster, role,
and security group survive between runs (deleted by `terraform destroy`).

## Costs

The Fargate task runs at `0.256 vCPU / 0.5 GB` for the test duration
(typically a couple of minutes). The IAM role, security group, and empty
ECS cluster have no standing cost.
