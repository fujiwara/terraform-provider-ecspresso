# Changelog

## [v0.0.6](https://github.com/fujiwara/terraform-provider-ecspresso/compare/v0.0.5...v0.0.6) - 2026-05-29
- Make from-scratch plan→apply work with config-level tfstate by @fujiwara in https://github.com/fujiwara/terraform-provider-ecspresso/pull/23

## [v0.0.5](https://github.com/fujiwara/terraform-provider-ecspresso/compare/v0.0.4...v0.0.5) - 2026-05-29
- Bump ecspresso to pre-v3 branch HEAD by @fujiwara in https://github.com/fujiwara/terraform-provider-ecspresso/pull/18
- Inject tfstate via WithPluginInstance by @fujiwara in https://github.com/fujiwara/terraform-provider-ecspresso/pull/20
- Bump aws-actions/configure-aws-credentials to v6.1.1 by @fujiwara in https://github.com/fujiwara/terraform-provider-ecspresso/pull/21
- Align docs with WithPluginInstance and warn on tfstate_func_prefix mismatch by @fujiwara in https://github.com/fujiwara/terraform-provider-ecspresso/pull/22

## [v0.0.4](https://github.com/fujiwara/terraform-provider-ecspresso/compare/v0.0.3...v0.0.4) - 2026-05-18
- README: mark Status as Published on the Terraform Registry by @fujiwara in https://github.com/fujiwara/terraform-provider-ecspresso/pull/5
- Generate Terraform Registry documentation by @fujiwara in https://github.com/fujiwara/terraform-provider-ecspresso/pull/6
- Add acceptance test scaffold (TF_ACC=1) by @fujiwara in https://github.com/fujiwara/terraform-provider-ecspresso/pull/9
- Add workflow_dispatch acceptance test workflow by @fujiwara in https://github.com/fujiwara/terraform-provider-ecspresso/pull/10
- fix tfstate_key defaults by @fujiwara in https://github.com/fujiwara/terraform-provider-ecspresso/pull/11
- DESIGN.md: reflect Phase 5–7 progress and drop plan-time validation pursuit by @fujiwara in https://github.com/fujiwara/terraform-provider-ecspresso/pull/8
- Resolve tfstate lookups from tfstate_values only by @fujiwara in https://github.com/fujiwara/terraform-provider-ecspresso/pull/12
- Skip ecspresso deploy when there's no diff against AWS by @fujiwara in https://github.com/fujiwara/terraform-provider-ecspresso/pull/13
- Bump kayac/ecspresso/v2 to current v2 HEAD by @fujiwara in https://github.com/fujiwara/terraform-provider-ecspresso/pull/14
- acc-test: populate tfstate_values from bootstrap tfstate by @fujiwara in https://github.com/fujiwara/terraform-provider-ecspresso/pull/15
- oidc: extend policy for diff and autoscaling describe by @fujiwara in https://github.com/fujiwara/terraform-provider-ecspresso/pull/16
- Surface bundled ecspresso version by @fujiwara in https://github.com/fujiwara/terraform-provider-ecspresso/pull/17

## [v0.0.3](https://github.com/fujiwara/terraform-provider-ecspresso/compare/v0.0.2...v0.0.3) - 2026-05-17

## [v0.0.2](https://github.com/fujiwara/terraform-provider-ecspresso/compare/v0.0.1...v0.0.2) - 2026-05-17

## [v0.0.1](https://github.com/fujiwara/terraform-provider-ecspresso/commits/v0.0.1) - 2026-05-17
