local tfstate = std.native('tfstate');

{
  launchType: 'FARGATE',
  // Keep desiredCount at 0 — the acceptance test only needs the
  // service to exist for Create / Read / Delete; running real Fargate
  // tasks adds cost and slows the test loop with no extra coverage.
  desiredCount: 0,
  networkConfiguration: {
    awsvpcConfiguration: {
      // Use the first subnet from the default VPC. tfstate('output.subnet_ids')
      // returns the list as-is, so index into it on the jsonnet side.
      subnets: [tfstate('output.subnet_ids')[0]],
      securityGroups: [tfstate('output.security_group_id')],
      assignPublicIp: 'ENABLED',
    },
  },
}
