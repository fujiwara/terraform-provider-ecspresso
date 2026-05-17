resource "ecspresso_service" "app" {
  config_path = "${path.module}/ecspresso.yml"

  # A diff in any of these values causes Terraform to re-run `ecspresso deploy`.
  tfstate_values = {
    "aws_lb_target_group.app.arn" = aws_lb_target_group.app.arn
    "aws_iam_role.task.arn"       = aws_iam_role.task.arn
  }
}
