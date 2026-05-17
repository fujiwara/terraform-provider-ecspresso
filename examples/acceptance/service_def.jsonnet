local tfstate = std.native('tfstate');

{
  launchType: 'FARGATE',
  desiredCount: 1,
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
