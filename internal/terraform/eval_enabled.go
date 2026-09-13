// Copyright IBM Corp. 2014, 2026
// SPDX-License-Identifier: BUSL-1.1

package terraform

import (
	"github.com/zclconf/go-cty/cty"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/terraform/internal/lang/marks"
	"github.com/hashicorp/terraform/internal/tfdiags"
)

// evaluateEnabledExpression interprets an "enabled" argument to decide
// whether a resource or module's single instance should exist. Like a
// resource with no count/for_each, it's addressed without an index.
//
// enabled accepts any value and never errors: null, false, "", and empty
// collections mean disabled; anything else, including 0, means enabled.
// A nullable variable can be used directly, with no comparison:
//
//	resource "aws_sns_topic_subscription" "alarm" {
//	  enabled  = var.alarm_email
//	  endpoint = var.alarm_email
//	}
//
// This only fits "exists when a value is present." It's not a fit for
// "exists when a value is absent" (e.g. a module creating a fallback
// resource only if the caller didn't supply one) - a bare value can't
// distinguish those intents. That case belongs inside the module that
// creates the fallback, deciding internally from the override it's given.
func evaluateEnabledExpression(expr hcl.Expression, ctx EvalContext, allowUnknown bool) (enabled bool, ok bool, diags tfdiags.Diagnostics) {
	enabledVal, valDiags := evaluateEnabledExpressionValue(expr, ctx)
	diags = diags.Append(valDiags)
	if diags.HasErrors() {
		return false, false, diags
	}

	if !enabledVal.IsKnown() {
		if !allowUnknown {
			diags = diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Invalid enabled argument",
				Detail:   `The "enabled" value depends on resource attributes that cannot be determined until apply, so Terraform cannot predict whether this resource will exist. To work around this, use the -target argument to first apply only the resources that "enabled" depends on.`,
				Subject:  expr.Range().Ptr(),
				Extra:    diagnosticCausedByUnknown(true),
			})
		}
		return false, false, diags
	}

	return enabledVal.True(), true, diags
}

// evaluateEnabledExpressionValue is like evaluateEnabledExpression but
// returns a cty.Bool value (possibly unknown) instead of a plain bool.
func evaluateEnabledExpressionValue(expr hcl.Expression, ctx EvalContext) (cty.Value, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	nullEnabled := cty.NullVal(cty.Bool)
	if expr == nil {
		return nullEnabled, nil
	}

	// No forced cty.Bool conversion: any type is accepted, so a nullable
	// non-boolean variable can be used directly.
	rawVal, enabledDiags := ctx.EvaluateExpr(expr, cty.DynamicPseudoType, nil)
	diags = diags.Append(enabledDiags)
	if diags.HasErrors() {
		return nullEnabled, diags
	}

	if marks.Has(rawVal, marks.Ephemeral) {
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Invalid enabled argument",
			Detail:   `The given "enabled" is derived from an ephemeral value, which means that Terraform cannot persist it between plan/apply rounds. Use only non-ephemeral values to control whether a resource exists.`,
			Subject:  expr.Range().Ptr(),
			Extra:    DiagnosticCausedByEphemeral(true),
		})
	}

	rawVal, deprecationDiags := ctx.Deprecations().ValidateAndUnmark(rawVal, ctx.Path().Module(), expr.Range().Ptr())
	diags = diags.Append(deprecationDiags)

	// Sensitivity isn't stripped here (unlike count): whether the instance
	// exists is always visible in the plan, so a sensitive value would leak.
	if marks.Has(rawVal, marks.Sensitive) {
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Invalid enabled argument",
			Detail:   `The "enabled" value is sensitive, but whether a resource is created or not is visible in the plan regardless of whether the resource's other attributes are hidden. Use a non-sensitive value here.`,
			Subject:  expr.Range().Ptr(),
		})
		return nullEnabled, diags
	}

	switch {
	case !rawVal.IsKnown():
		return cty.UnknownVal(cty.Bool), diags
	case rawVal.Type() == cty.Bool:
		return rawVal, diags
	default:
		return cty.BoolVal(!isEmptyEnabledValue(rawVal)), diags
	}
}

// isEmptyEnabledValue reports whether v counts as "nothing was provided"
// for an "enabled" expression: null, an empty string, or an empty
// collection. The number zero is deliberately excluded - it's too common
// a legitimate value (a count, an index) to treat as empty.
func isEmptyEnabledValue(v cty.Value) bool {
	switch {
	case v.IsNull():
		return true
	case v.Type() == cty.String:
		return v.AsString() == ""
	case v.Type().IsListType(), v.Type().IsSetType(), v.Type().IsMapType(),
		v.Type().IsTupleType(), v.Type().IsObjectType():
		return v.LengthInt() == 0
	default:
		return false
	}
}
