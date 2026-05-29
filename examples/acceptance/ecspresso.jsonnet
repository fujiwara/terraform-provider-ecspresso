// Acceptance test config for terraform-provider-ecspresso.
// Driven by `make acc-test`. The bootstrap stack
// (`bootstrap/main.tf`) is expected to be apply'd once and left in
// place.
//
// The provider injects an in-memory tfstate plugin backed by
// `tfstate_values` (ecspresso's WithPluginInstance,
// kayac/ecspresso#1031), so no `plugins:` block is needed and even
// config-level fields can use `tfstate(...)`. `cluster` is therefore
// resolved from the bootstrap stack's `output.cluster_name` to
// exercise that path; `service` is the name this test creates (the
// bootstrap stack does not), so it stays a plain literal.
local must_env = std.native('must_env');
local tfstate = std.native('tfstate');

{
  region: must_env('AWS_REGION'),
  cluster: tfstate('output.cluster_name'),
  service: 'ecspresso-provider-acc-test',
  service_definition: 'service_def.jsonnet',
  task_definition: 'taskdef.jsonnet',
}
