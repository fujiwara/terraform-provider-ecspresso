// Acceptance test config for terraform-provider-ecspresso.
// Driven by `make acc-test` after `terraform apply` in bootstrap/.
//
// Note: ecspresso config files cannot use the `tfstate` Jsonnet
// function — the plugin itself is declared here and is not yet
// initialised when the config is parsed. `cluster` is therefore
// hardcoded to match `bootstrap/main.tf`. The task/service
// definitions can use `tfstate(...)` because they are rendered after
// the plugin is set up.
local must_env = std.native('must_env');

{
  region: must_env('AWS_REGION'),
  cluster: 'ecspresso-provider-acc-test',
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
