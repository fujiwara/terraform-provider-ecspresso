// Acceptance test config for terraform-provider-ecspresso.
// Driven by `make acc-test`. The bootstrap stack
// (`bootstrap/main.tf`) is expected to be apply'd once and left in
// place; its tfstate is read from S3 here.
//
// Note: ecspresso config files cannot use the `tfstate` Jsonnet
// function — the plugin itself is declared here and is not yet
// initialised when the config is parsed. `cluster` / `service` are
// therefore hardcoded to match `bootstrap/main.tf`. The task and
// service definitions can use `tfstate(...)` because they are
// rendered after the plugin is set up.
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
        // The bootstrap stack stores its state in S3; set TFSTATE_URL
        // to that object's URL (e.g. s3://my-bucket/path/to/bootstrap.tfstate).
        url: must_env('TFSTATE_URL'),
      },
    },
  ],
}
