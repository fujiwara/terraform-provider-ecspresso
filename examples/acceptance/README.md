# Acceptance test fixture

Self-contained fixture for running `make acc-test` against real AWS.

The acceptance test in `internal/provider/service_resource_acc_test.go`
self-skips unless `ECSPRESSO_TEST_CONFIG_PATH` is set. This directory
provides that path plus a small Terraform stack that creates the
prerequisite AWS resources (an empty ECS cluster, task execution role,
security group) inside the default VPC.

The bootstrap stack is designed to be **applied once and left in
place** — every resource it creates is free at rest (no Fargate tasks,
no NAT, no ALB). Doing so lets each acceptance test run touch only the
ECS service, which keeps the required IAM permissions narrow and skips
a couple of minutes of `terraform apply` / `terraform destroy` per
run.

## Files

- `bootstrap/main.tf` — One-shot Terraform stack for the AWS
  prerequisites. State is stored in S3 so `ecspresso` running under CI
  can read it. Exports `cluster_name`, `task_execution_role_arn`,
  `subnet_ids`, `security_group_id`.
- `ecspresso.jsonnet` — ecspresso config. Reads the bootstrap stack's
  S3 tfstate via the `tfstate` plugin (URL passed in `TFSTATE_URL`).
- `taskdef.jsonnet` — Minimal Fargate task definition running one
  `public.ecr.aws/nginx/nginx:latest` container.
- `service_def.jsonnet` — Service definition; `desiredCount: 0`, so
  no Fargate tasks are launched during the test.
- `oidc/main.tf` — Optional. Provisions the GitHub Actions OIDC
  provider and the IAM role used by `.github/workflows/acc-test.yml`.
  Only needed if you intend to run the test through GitHub Actions.

## One-time setup

```sh
cd examples/acceptance/bootstrap
terraform init \
  -backend-config="bucket=<your-tfstate-bucket>" \
  -backend-config="key=terraform-provider-ecspresso/acceptance/bootstrap.tfstate" \
  -backend-config="region=<your-region>"
terraform apply
```

You'll need an S3 bucket that already exists; the bootstrap stack does
not create it for you (use one of your existing tfstate buckets).

## Run the acceptance test

```sh
export AWS_REGION=ap-northeast-1
export TFSTATE_URL=s3://<your-tfstate-bucket>/terraform-provider-ecspresso/acceptance/bootstrap.tfstate
export ECSPRESSO_TEST_CONFIG_PATH=$PWD/ecspresso.jsonnet   # if you're in this dir
cd ../..
make acc-test
```

The acceptance test creates the ECS service `ecspresso-provider-acc-test`
in the bootstrap cluster, asserts the resource's computed attributes
(`id`, `service_arn`, `service_name`, `cluster_arn`, `cluster_name`,
`last_apply_at`), and removes the service again. The cluster, role,
and security group survive between runs.

## Tear down

```sh
cd examples/acceptance/bootstrap
terraform destroy
```

You only need this when you no longer want the standing fixture.

## Costs

`desiredCount: 0` on the service means no Fargate tasks ever run, so
the AWS-side cost of an acceptance test run is effectively zero. The
standing fixture is also zero — empty ECS cluster, IAM role, and
security group have no charges. The S3 object holding the bootstrap
tfstate is the only ongoing item, and it's a few KB.
