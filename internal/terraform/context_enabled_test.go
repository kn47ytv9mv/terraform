// Copyright IBM Corp. 2014, 2026
// SPDX-License-Identifier: BUSL-1.1

package terraform

import (
	"fmt"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/hashicorp/terraform/internal/addrs"
	"github.com/hashicorp/terraform/internal/configs/configschema"
	"github.com/hashicorp/terraform/internal/plans"
	"github.com/hashicorp/terraform/internal/providers"
	testing_provider "github.com/hashicorp/terraform/internal/providers/testing"
	"github.com/hashicorp/terraform/internal/states"
	"github.com/hashicorp/terraform/internal/tfdiags"
)

// These tests exercise the "enabled" meta-argument, an alternative to
// `count = cond ? 1 : 0` that creates zero or one instances without an
// indexed instance address (see hashicorp/terraform#21953).

// assertOutputChangeEquals looks up the named output change in changes and
// fails the test unless it decodes to exactly want.
func assertOutputChangeEquals(t *testing.T, changes *plans.ChangesSrc, name string, want cty.Value) {
	t.Helper()
	for _, o := range changes.Outputs {
		if o.Addr.OutputValue.Name != name {
			continue
		}
		val, err := o.After.Decode(cty.DynamicPseudoType)
		if err != nil {
			t.Fatalf("decoding output %q: %s", name, err)
		}
		if !val.RawEquals(want) {
			t.Fatalf("output %q: got %#v, want %#v", name, val, want)
		}
		return
	}
	t.Fatalf("no %q output change in plan", name)
}

func TestContext2Plan_enabledResourceTrue(t *testing.T) {
	m := testModuleInline(t, map[string]string{
		"main.tf": `
terraform {
  experiments = [enabled_meta_argument]
}

resource "aws_instance" "foo" {
  enabled = true
}
`,
	})
	p := testProvider("aws")
	p.PlanResourceChangeFn = testDiffFn
	ctx := testContext2(t, &ContextOpts{
		Providers: map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("aws"): testProviderFuncFixed(p),
		},
	})

	plan, diags := ctx.Plan(m, states.NewState(), DefaultPlanOpts)
	tfdiags.AssertNoErrors(t, diags)

	if len(plan.Changes.Resources) != 1 {
		t.Fatalf("expected 1 change, got %d", len(plan.Changes.Resources))
	}
	rc := plan.Changes.Resources[0]
	if rc.Action != plans.Create {
		t.Fatalf("expected create, got %s", rc.Action)
	}

	// The crucial assertion: the address must NOT carry an instance key.
	// This is the entire point of "enabled" over `count`/`for_each`.
	if rc.Addr.String() != "aws_instance.foo" {
		t.Fatalf("expected unindexed address %q, got %q", "aws_instance.foo", rc.Addr.String())
	}
	if rc.Addr.Resource.Key != addrs.NoKey {
		t.Fatalf("expected NoKey instance key, got %#v", rc.Addr.Resource.Key)
	}
}

func TestContext2Plan_enabledResourceFalse(t *testing.T) {
	m := testModuleInline(t, map[string]string{
		"main.tf": `
terraform {
  experiments = [enabled_meta_argument]
}

resource "aws_instance" "foo" {
  enabled = false
}
`,
	})
	p := testProvider("aws")
	p.PlanResourceChangeFn = testDiffFn
	ctx := testContext2(t, &ContextOpts{
		Providers: map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("aws"): testProviderFuncFixed(p),
		},
	})

	plan, diags := ctx.Plan(m, states.NewState(), DefaultPlanOpts)
	tfdiags.AssertNoErrors(t, diags)

	if len(plan.Changes.Resources) != 0 {
		t.Fatalf("expected 0 changes, got %d: %#v", len(plan.Changes.Resources), plan.Changes.Resources)
	}
}

// TestContext2Apply_enabledToggle applies with enabled = true, then flips
// to false and confirms the instance is destroyed and state ends up empty.
func TestContext2Apply_enabledToggle(t *testing.T) {
	p := testProvider("aws")
	p.PlanResourceChangeFn = testDiffFn
	p.ApplyResourceChangeFn = testApplyFn
	ctx := testContext2(t, &ContextOpts{
		Providers: map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("aws"): testProviderFuncFixed(p),
		},
	})

	mOn := testModuleInline(t, map[string]string{
		"main.tf": `
terraform {
  experiments = [enabled_meta_argument]
}

resource "aws_instance" "foo" {
  enabled = true
}
`,
	})

	plan, diags := ctx.Plan(mOn, states.NewState(), DefaultPlanOpts)
	tfdiags.AssertNoErrors(t, diags)

	state, diags := ctx.Apply(plan, mOn, nil)
	if diags.HasErrors() {
		t.Fatalf("apply (enabled) diags: %s", diags.Err())
	}
	if len(state.RootModule().Resources) != 1 {
		t.Fatalf("expected 1 resource in state after enabling, got %d", len(state.RootModule().Resources))
	}

	mOff := testModuleInline(t, map[string]string{
		"main.tf": `
terraform {
  experiments = [enabled_meta_argument]
}

resource "aws_instance" "foo" {
  enabled = false
}
`,
	})

	plan2, diags := ctx.Plan(mOff, state, DefaultPlanOpts)
	tfdiags.AssertNoErrors(t, diags)

	if len(plan2.Changes.Resources) != 1 || plan2.Changes.Resources[0].Action != plans.Delete {
		t.Fatalf("expected a single destroy change, got %#v", plan2.Changes.Resources)
	}

	state2, diags := ctx.Apply(plan2, mOff, nil)
	if diags.HasErrors() {
		t.Fatalf("apply (disabled) diags: %s", diags.Err())
	}
	if len(state2.RootModule().Resources) != 0 {
		t.Fatalf("expected empty state after disabling, got %#v", state2.RootModule().Resources)
	}
}

// TestContext2Plan_enabledDisabledReferenceIsNull confirms a disabled
// resource evaluates to null, so try()/coalesce() and ternaries still work.
func TestContext2Plan_enabledDisabledReferenceIsNull(t *testing.T) {
	m := testModuleInline(t, map[string]string{
		"main.tf": `
terraform {
  experiments = [enabled_meta_argument]
}

resource "aws_instance" "foo" {
  enabled = false
}

output "direct" {
  value = try(aws_instance.foo.id, "fallback")
}

output "guarded" {
  value = false ? aws_instance.foo.id : "fallback"
}
`,
	})
	p := testProvider("aws")
	p.PlanResourceChangeFn = testDiffFn
	ctx := testContext2(t, &ContextOpts{
		Providers: map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("aws"): testProviderFuncFixed(p),
		},
	})

	plan, diags := ctx.Plan(m, states.NewState(), DefaultPlanOpts)
	tfdiags.AssertNoErrors(t, diags)

	for _, name := range []string{"direct", "guarded"} {
		assertOutputChangeEquals(t, plan.Changes, name, cty.StringVal("fallback"))
	}
}

// TestContext2Plan_enabledModule mirrors the resource-level tests for a
// module call.
func TestContext2Plan_enabledModule(t *testing.T) {
	p := testProvider("aws")
	p.PlanResourceChangeFn = testDiffFn
	ctx := testContext2(t, &ContextOpts{
		Providers: map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("aws"): testProviderFuncFixed(p),
		},
	})

	m := testModuleInline(t, map[string]string{
		"main.tf": `
terraform {
  experiments = [enabled_meta_argument]
}

module "child" {
  source  = "./child"
  enabled = true
}
`,
		"child/main.tf": `
resource "aws_instance" "foo" {}

output "id" {
  value = aws_instance.foo.id
}
`,
	})

	plan, diags := ctx.Plan(m, states.NewState(), DefaultPlanOpts)
	tfdiags.AssertNoErrors(t, diags)

	if len(plan.Changes.Resources) != 1 {
		t.Fatalf("expected 1 change, got %d", len(plan.Changes.Resources))
	}
	if got, want := plan.Changes.Resources[0].Addr.String(), "module.child.aws_instance.foo"; got != want {
		t.Fatalf("expected unindexed module instance address %q, got %q", want, got)
	}

	mDisabled := testModuleInline(t, map[string]string{
		"main.tf": `
terraform {
  experiments = [enabled_meta_argument]
}

module "child" {
  source  = "./child"
  enabled = false
}

output "borrowed" {
  # Must be null, not an empty list/tuple, so try() works.
  value = try(module.child.id, "fallback")
}
`,
		"child/main.tf": `
resource "aws_instance" "foo" {}

output "id" {
  value = aws_instance.foo.id
}
`,
	})

	plan2, diags := ctx.Plan(mDisabled, states.NewState(), DefaultPlanOpts)
	tfdiags.AssertNoErrors(t, diags)

	if len(plan2.Changes.Resources) != 0 {
		t.Fatalf("expected 0 changes with module disabled, got %d: %#v", len(plan2.Changes.Resources), plan2.Changes.Resources)
	}

	assertOutputChangeEquals(t, plan2.Changes, "borrowed", cty.StringVal("fallback"))
}

// TestContext2Plan_enabledDataResource confirms "enabled" works the same
// for data resources as for managed resources.
func TestContext2Plan_enabledDataResource(t *testing.T) {
	p := simpleMockProvider()
	p.ReadDataSourceFn = func(req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
		return providers.ReadDataSourceResponse{
			State: cty.ObjectVal(map[string]cty.Value{
				"test_string": cty.StringVal("read"),
				"test_number": cty.NumberIntVal(1),
				"test_bool":   cty.True,
				"test_list":   cty.ListValEmpty(cty.String),
				"test_map":    cty.MapValEmpty(cty.String),
			}),
		}
	}
	ctx := testContext2(t, &ContextOpts{
		Providers: map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
		},
	})

	mOn := testModuleInline(t, map[string]string{
		"main.tf": `
terraform {
  experiments = [enabled_meta_argument]
}

data "test_object" "foo" {
  enabled = true
}
`,
	})
	plan, diags := ctx.Plan(mOn, states.NewState(), DefaultPlanOpts)
	tfdiags.AssertNoErrors(t, diags)

	// A data source read isn't a "change" the way create/update/destroy
	// are, so it won't appear in plan.Changes.Resources - check state
	// after apply instead, the same way a plain (non-"enabled") data
	// source read would be verified.
	state, diags := ctx.Apply(plan, mOn, nil)
	if diags.HasErrors() {
		t.Fatalf("apply diags: %s", diags.Err())
	}
	if _, ok := state.RootModule().Resources["data.test_object.foo"]; !ok {
		t.Fatalf("expected unindexed address %q in state, got %#v", "data.test_object.foo", state.RootModule().Resources)
	}

	mOff := testModuleInline(t, map[string]string{
		"main.tf": `
terraform {
  experiments = [enabled_meta_argument]
}

data "test_object" "foo" {
  enabled = false
}

output "borrowed" {
  value = try(data.test_object.foo.test_string, "fallback")
}
`,
	})
	plan2, diags := ctx.Plan(mOff, states.NewState(), DefaultPlanOpts)
	tfdiags.AssertNoErrors(t, diags)

	state2, diags := ctx.Apply(plan2, mOff, nil)
	if diags.HasErrors() {
		t.Fatalf("apply diags: %s", diags.Err())
	}
	if _, ok := state2.RootModule().Resources["data.test_object.foo"]; ok {
		t.Fatal("expected no data.test_object.foo in state with enabled = false")
	}

	assertOutputChangeEquals(t, plan2.Changes, "borrowed", cty.StringVal("fallback"))
}

// TestContext2Plan_enabledEphemeralResource confirms "enabled" works the
// same for ephemeral resources.
func TestContext2Plan_enabledEphemeralResource(t *testing.T) {
	ephem := &testing_provider.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			EphemeralResourceTypes: map[string]providers.Schema{
				"ephem_resource": {
					Body: &configschema.Block{
						Attributes: map[string]*configschema.Attribute{
							"value": {
								Type:     cty.String,
								Computed: true,
							},
						},
					},
				},
			},
		},
	}
	ephem.OpenEphemeralResourceFn = func(providers.OpenEphemeralResourceRequest) (resp providers.OpenEphemeralResourceResponse) {
		resp.Result = cty.ObjectVal(map[string]cty.Value{
			"value": cty.StringVal("test string"),
		})
		return resp
	}

	p := simpleMockProvider()
	var gotConfig string
	p.ConfigureProviderFn = func(req providers.ConfigureProviderRequest) (resp providers.ConfigureProviderResponse) {
		gotConfig = req.Config.GetAttr("test_string").AsString()
		return resp
	}

	ctx := testContext2(t, &ContextOpts{
		Providers: map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("ephem"): testProviderFuncFixed(ephem),
			addrs.NewDefaultProvider("test"):  testProviderFuncFixed(p),
		},
	})

	mOn := testModuleInline(t, map[string]string{
		"main.tf": `
terraform {
  experiments = [enabled_meta_argument]
}

ephemeral "ephem_resource" "data" {
  enabled = true
}

provider "test" {
  test_string = ephemeral.ephem_resource.data.value
}

resource "test_object" "obj" {}
`,
	})
	plan, diags := ctx.Plan(mOn, states.NewState(), DefaultPlanOpts)
	tfdiags.AssertNoErrors(t, diags)

	// Ephemeral resources are transient and never appear in
	// plan.Changes.Resources or in state, so the address/singleton
	// assertion has to be functional: the provider config block below
	// only successfully resolves ephemeral.ephem_resource.data.value
	// (unindexed) if the resource was correctly registered as a single,
	// NoKey instance.
	_, diags = ctx.Apply(plan, mOn, nil)
	if diags.HasErrors() {
		t.Fatalf("apply diags: %s", diags.Err())
	}
	if gotConfig != "test string" {
		t.Fatalf("provider did not receive the ephemeral value: got %q", gotConfig)
	}

	mOff := testModuleInline(t, map[string]string{
		"main.tf": `
terraform {
  experiments = [enabled_meta_argument]
}

ephemeral "ephem_resource" "data" {
  enabled = false
}

provider "test" {
  # Must not error: a disabled ephemeral resource evaluates to null, and
  # try() catches the resulting "attribute access on null" error.
  test_string = try(ephemeral.ephem_resource.data.value, "fallback")
}

resource "test_object" "obj" {}
`,
	})
	plan2, diags := ctx.Plan(mOff, states.NewState(), DefaultPlanOpts)
	tfdiags.AssertNoErrors(t, diags)

	gotConfig = ""
	_, diags = ctx.Apply(plan2, mOff, nil)
	if diags.HasErrors() {
		t.Fatalf("apply diags: %s", diags.Err())
	}
	if gotConfig != "fallback" {
		t.Fatalf("expected provider to receive the try() fallback, got %q", gotConfig)
	}
}

// TestContext2Plan_enabledImpliedMoveFromCount confirms switching a
// resource from `count = 1` to `enabled = true` migrates automatically,
// with no `moved` block needed, since both produce a NoKey address.
func TestContext2Plan_enabledImpliedMoveFromCount(t *testing.T) {
	state := states.NewState()
	root := state.EnsureModule(addrs.RootModuleInstance)
	root.SetResourceInstanceCurrent(
		mustResourceInstanceAddr("aws_instance.foo[0]").Resource,
		&states.ResourceInstanceObjectSrc{
			Status:    states.ObjectReady,
			AttrsJSON: []byte(`{"id":"bar", "foo": "foo", "type": "aws_instance"}`),
		},
		mustProviderConfig(`provider["registry.terraform.io/hashicorp/aws"]`),
	)

	m := testModuleInline(t, map[string]string{
		"main.tf": `
terraform {
  experiments = [enabled_meta_argument]
}

resource "aws_instance" "foo" {
  enabled = true
  foo     = "foo"
}
`,
	})
	p := testProvider("aws")
	p.PlanResourceChangeFn = testDiffFn
	ctx := testContext2(t, &ContextOpts{
		Providers: map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("aws"): testProviderFuncFixed(p),
		},
	})

	plan, diags := ctx.Plan(m, state, DefaultPlanOpts)
	tfdiags.AssertNoErrors(t, diags)

	// The old aws_instance.foo[0] address should be gone entirely (moved,
	// not deleted-and-recreated), and the new aws_instance.foo address
	// should show no changes at all - proof that the existing object was
	// carried over via the move rather than destroyed and re-created.
	if change := plan.Changes.ResourceInstance(mustResourceInstanceAddr("aws_instance.foo[0]")); change != nil {
		t.Fatalf("unexpected change for the old aws_instance.foo[0] address: %#v", change)
	}
	change := plan.Changes.ResourceInstance(mustResourceInstanceAddr("aws_instance.foo"))
	if change == nil {
		t.Fatal("no planned change for aws_instance.foo")
	}
	if got, want := change.Action, plans.NoOp; got != want {
		t.Fatalf("wrong action for aws_instance.foo: got %s, want %s (i.e. the implied move should have carried the object over unchanged)", got, want)
	}
}

// TestContext2Plan_selfReferencesEnabled mirrors
// TestContext2Plan_selfReferences (context_plan2_test.go) for "enabled".
// Kept separate since the experiment warning would break that test's
// shared diagnostic-count assertion.
func TestContext2Plan_selfReferencesEnabled(t *testing.T) {
	tcs := []struct {
		attribute string
	}{
		{
			attribute: "enabled = test_object.a.test_string == \"\"",
		},
		// Self-references must be rejected even under try()/can() -
		// enabled follows the same rule count/for_each already do.
		{
			attribute: "enabled = try(test_object.a.test_string, \"\") == \"\"",
		},
	}
	for _, tc := range tcs {
		t.Run(tc.attribute, func(t *testing.T) {
			tmpl := `
terraform {
  experiments = [enabled_meta_argument]
}

resource "test_object" "a" {
  %%attribute%%
}
`
			module := strings.ReplaceAll(tmpl, "%%attribute%%", tc.attribute)
			m := testModuleInline(t, map[string]string{
				"main.tf": module,
			})

			p := simpleMockProvider()
			ctx := testContext2(t, &ContextOpts{
				Providers: map[addrs.Provider]providers.Factory{
					// The provider is never actually going to get called
					// here, we should catch the error long before anything
					// happens.
					addrs.NewDefaultProvider("test"): testProviderFuncFixed(p),
				},
			})

			_, diags := ctx.Plan(m, states.NewState(), DefaultPlanOpts)

			var found bool
			for _, diag := range diags {
				if diag.Description().Summary == "Self-referential block" {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected a \"Self-referential block\" diagnostic, got: %s", diags.ErrWithWarnings())
			}
		})
	}
}

// TestContext2Plan_enabledDirectNullable confirms a nullable, non-boolean
// variable works directly as "enabled", with no comparison needed.
func TestContext2Plan_enabledDirectNullable(t *testing.T) {
	p := testProvider("aws")
	p.PlanResourceChangeFn = testDiffFn
	ctx := testContext2(t, &ContextOpts{
		Providers: map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("aws"): testProviderFuncFixed(p),
		},
	})

	mUnset := testModuleInline(t, map[string]string{
		"main.tf": `
terraform {
  experiments = [enabled_meta_argument]
}

variable "alarm_email" {
  default = null
}

resource "aws_instance" "alarm" {
  enabled = var.alarm_email
}
`,
	})
	plan, diags := ctx.Plan(mUnset, states.NewState(), &PlanOpts{
		Mode: plans.NormalMode,
		SetVariables: InputValues{
			// NilVal, not omitted: every declared root variable needs an
			// explicit entry, even to just request its config default.
			"alarm_email": &InputValue{Value: cty.NilVal, SourceType: ValueFromCaller},
		},
	})
	tfdiags.AssertNoErrors(t, diags)
	if len(plan.Changes.Resources) != 0 {
		t.Fatalf("expected 0 changes with var.alarm_email unset, got %d: %#v", len(plan.Changes.Resources), plan.Changes.Resources)
	}

	mSet := testModuleInline(t, map[string]string{
		"main.tf": `
terraform {
  experiments = [enabled_meta_argument]
}

variable "alarm_email" {
  default = null
}

resource "aws_instance" "alarm" {
  enabled = var.alarm_email
}
`,
	})
	plan2, diags := ctx.Plan(mSet, states.NewState(), &PlanOpts{
		Mode: plans.NormalMode,
		SetVariables: InputValues{
			"alarm_email": &InputValue{Value: cty.StringVal("me@example.com"), SourceType: ValueFromCaller},
		},
	})
	tfdiags.AssertNoErrors(t, diags)
	if len(plan2.Changes.Resources) != 1 {
		t.Fatalf("expected 1 change with var.alarm_email set, got %d", len(plan2.Changes.Resources))
	}
	if got, want := plan2.Changes.Resources[0].Addr.String(), "aws_instance.alarm"; got != want {
		t.Fatalf("expected unindexed address %q, got %q", want, got)
	}
}

// TestContext2Plan_enabledFalsySet confirms the full falsy set (null,
// false, "", empty collections), that zero is excluded, and that none of
// this ever errors.
func TestContext2Plan_enabledFalsySet(t *testing.T) {
	p := testProvider("aws")
	p.PlanResourceChangeFn = testDiffFn

	tcs := []struct {
		name        string
		expr        string
		wantEnabled bool
	}{
		{"null", "null", false},
		{"false", "false", false},
		{"empty string", `""`, false},
		{"empty list", "[]", false},
		{"empty map", "{}", false},
		{"true", "true", true},
		{"non-empty string", `"x"`, true},
		{"non-empty list", `["x"]`, true},
		{"zero (deliberately NOT falsy)", "0", true},
	}
	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			ctx := testContext2(t, &ContextOpts{
				Providers: map[addrs.Provider]providers.Factory{
					addrs.NewDefaultProvider("aws"): testProviderFuncFixed(p),
				},
			})
			m := testModuleInline(t, map[string]string{
				"main.tf": fmt.Sprintf(`
terraform {
  experiments = [enabled_meta_argument]
}

resource "aws_instance" "foo" {
  enabled = %s
}
`, tc.expr),
			})
			plan, diags := ctx.Plan(m, states.NewState(), DefaultPlanOpts)
			tfdiags.AssertNoErrors(t, diags)

			gotEnabled := len(plan.Changes.Resources) == 1
			if gotEnabled != tc.wantEnabled {
				t.Fatalf("enabled = %s: got %d changes (enabled=%v), want enabled=%v", tc.expr, len(plan.Changes.Resources), gotEnabled, tc.wantEnabled)
			}
		})
	}
}
