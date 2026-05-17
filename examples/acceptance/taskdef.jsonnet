local tfstate = std.native('tfstate');

{
  family: 'ecspresso-provider-acc-test',
  networkMode: 'awsvpc',
  requiresCompatibilities: ['FARGATE'],
  cpu: '256',
  memory: '512',
  executionRoleArn: tfstate('output.task_execution_role_arn'),
  containerDefinitions: [
    {
      name: 'app',
      image: 'public.ecr.aws/nginx/nginx:latest',
      essential: true,
    },
  ],
}
