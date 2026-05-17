terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

provider "aws" {}

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
  # ECS: cluster + service + task definition lifecycle.
  statement {
    effect = "Allow"
    actions = [
      "ecs:CreateCluster",
      "ecs:DeleteCluster",
      "ecs:DescribeClusters",
      "ecs:CreateService",
      "ecs:DescribeServices",
      "ecs:UpdateService",
      "ecs:DeleteService",
      "ecs:RegisterTaskDefinition",
      "ecs:DescribeTaskDefinition",
      "ecs:DeregisterTaskDefinition",
      "ecs:ListTaskDefinitions",
      "ecs:TagResource",
      "ecs:UntagResource",
    ]
    resources = ["*"]
  }

  # IAM: manage the task execution role the bootstrap stack creates,
  # and PassRole it through to ECS at deploy time.
  statement {
    effect = "Allow"
    actions = [
      "iam:CreateRole",
      "iam:DeleteRole",
      "iam:GetRole",
      "iam:PassRole",
      "iam:AttachRolePolicy",
      "iam:DetachRolePolicy",
      "iam:ListAttachedRolePolicies",
      "iam:ListRolePolicies",
      "iam:ListInstanceProfilesForRole",
      "iam:TagRole",
      "iam:UntagRole",
    ]
    resources = ["*"]
  }

  # EC2: default-VPC lookup and security group CRUD.
  statement {
    effect = "Allow"
    actions = [
      "ec2:DescribeVpcs",
      "ec2:DescribeSubnets",
      "ec2:DescribeSecurityGroups",
      "ec2:CreateSecurityGroup",
      "ec2:DeleteSecurityGroup",
      "ec2:AuthorizeSecurityGroupEgress",
      "ec2:RevokeSecurityGroupEgress",
      "ec2:AuthorizeSecurityGroupIngress",
      "ec2:RevokeSecurityGroupIngress",
      "ec2:CreateTags",
      "ec2:DeleteTags",
      "ec2:DescribeTags",
      "ec2:DescribeNetworkInterfaces",
    ]
    resources = ["*"]
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
