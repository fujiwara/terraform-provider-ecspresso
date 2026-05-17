terraform {
  backend "s3" {}

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

provider "aws" {
  default_tags {
    tags = {
      Project   = "terraform-provider-ecspresso"
      Purpose   = "acceptance-test"
      ManagedBy = "terraform"
    }
  }
}

variable "github_repo" {
  description = "GitHub repository slug in 'owner/name' form that is allowed to assume the role."
  type        = string
  default     = "fujiwara/terraform-provider-ecspresso"
}

variable "environment_name" {
  description = "GitHub Environment that is allowed to assume the role. Must match `.github/workflows/acc-test.yml`'s `environment:` value."
  type        = string
  default     = "acc-test"
}

variable "tfstate_bucket" {
  description = "S3 bucket holding the bootstrap stack's tfstate object."
  type        = string
}

variable "tfstate_key" {
  description = "S3 object key of the bootstrap stack's tfstate."
  type        = string
  default     = "terraform-provider-ecspresso/acceptance/bootstrap.tfstate"
}

locals {
  cluster_name        = "ecspresso-provider-acc-test"
  task_execution_role = "ecspresso-provider-acc-test-task-execution"
}

# GitHub Actions OIDC provider. AWS allows only one per account for a
# given URL; see the README for how to handle the case where it already
# exists (data lookup or terraform import).
resource "aws_iam_openid_connect_provider" "github" {
  url            = "https://token.actions.githubusercontent.com"
  client_id_list = ["sts.amazonaws.com"]
  # Thumbprint of the certificate chain that issues GitHub Actions
  # tokens. AWS auto-rotates this value on the server side, so the
  # literal here only matters at first creation.
  thumbprint_list = ["6938fd4d98bab03faadb97b34396831e3780aea1"]
}

data "aws_iam_policy_document" "assume_role" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.github.arn]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }

    condition {
      test     = "StringLike"
      variable = "token.actions.githubusercontent.com:sub"
      values   = ["repo:${var.github_repo}:environment:${var.environment_name}"]
    }
  }
}

resource "aws_iam_role" "github_acc_test" {
  name               = "terraform-provider-ecspresso-acc-test"
  description        = "Assumed by GitHub Actions to run terraform-provider-ecspresso acceptance tests."
  assume_role_policy = data.aws_iam_policy_document.assume_role.json
}

data "aws_iam_policy_document" "acc_test" {
  # ECS service lifecycle, scoped to the acceptance test cluster.
  # ecspresso-provider-acc-test is the cluster the bootstrap stack
  # creates; ecspresso only ever operates on it.
  statement {
    effect = "Allow"
    actions = [
      "ecs:CreateService",
      "ecs:DescribeServices",
      "ecs:UpdateService",
      "ecs:DeleteService",
    ]
    resources = ["arn:aws:ecs:*:*:service/${local.cluster_name}/*"]
  }

  # Cluster describe is needed by ecspresso during deploy. Scoped
  # to the acceptance test cluster only.
  statement {
    effect    = "Allow"
    actions   = ["ecs:DescribeClusters"]
    resources = ["arn:aws:ecs:*:*:cluster/${local.cluster_name}"]
  }

  # Task definition register / list cannot be resource-restricted; ECS
  # only supports resource-level restrictions on describe / deregister.
  statement {
    effect = "Allow"
    actions = [
      "ecs:RegisterTaskDefinition",
      "ecs:ListTaskDefinitions",
    ]
    resources = ["*"]
  }

  statement {
    effect = "Allow"
    actions = [
      "ecs:DescribeTaskDefinition",
      "ecs:DeregisterTaskDefinition",
    ]
    resources = ["arn:aws:ecs:*:*:task-definition/${local.cluster_name}:*"]
  }

  statement {
    effect = "Allow"
    actions = [
      "ecs:TagResource",
      "ecs:UntagResource",
    ]
    resources = ["*"]
  }

  # IAM: only PassRole for the task execution role the bootstrap stack
  # already provisioned, and only when ECS is the consumer. No
  # CreateRole / AttachPolicy / etc. — the bootstrap is applied once
  # out-of-band by a human and is not managed by this role.
  statement {
    effect    = "Allow"
    actions   = ["iam:PassRole"]
    resources = ["arn:aws:iam::*:role/${local.task_execution_role}"]
    condition {
      test     = "StringEquals"
      variable = "iam:PassedToService"
      values   = ["ecs-tasks.amazonaws.com"]
    }
  }

  # EC2 read-only: ecspresso resolves VPC / subnet / SG / ENI during
  # deploy and verify.
  statement {
    effect = "Allow"
    actions = [
      "ec2:DescribeVpcs",
      "ec2:DescribeSubnets",
      "ec2:DescribeSecurityGroups",
      "ec2:DescribeNetworkInterfaces",
    ]
    resources = ["*"]
  }

  # S3: read the bootstrap stack's tfstate so ecspresso's tfstate
  # plugin can resolve the outputs (role ARN, subnet IDs, SG ID).
  statement {
    effect    = "Allow"
    actions   = ["s3:GetObject"]
    resources = ["arn:aws:s3:::${var.tfstate_bucket}/${var.tfstate_key}"]
  }
}

resource "aws_iam_role_policy" "acc_test" {
  name   = "acc-test"
  role   = aws_iam_role.github_acc_test.id
  policy = data.aws_iam_policy_document.acc_test.json
}

output "role_arn" {
  description = "Set this as AWS_ROLE_ARN on the acc-test GitHub Environment."
  value       = aws_iam_role.github_acc_test.arn
}
