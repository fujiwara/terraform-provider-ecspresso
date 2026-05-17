// Acceptance test config for terraform-provider-ecspresso.
// Driven by `make acc-test` after `terraform apply` in bootstrap/.
local must_env = std.native('must_env');
local tfstate = std.native('tfstate');

{
  region: must_env('AWS_REGION'),
  cluster: tfstate('output.cluster_name'),
  service: 'ecspresso-provider-acc-test',
  service_definition: 'service_def.jsonnet',
  task_definition: 'taskdef.jsonnet',
  plugins: [
    {
      name: 'tfstate',
      config: {
        // The bootstrap terraform stack sits next to this file.
        path: 'bootstrap/terraform.tfstate',
      },
    },
  ],
}
