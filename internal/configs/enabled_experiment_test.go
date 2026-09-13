// Copyright IBM Corp. 2014, 2026
// SPDX-License-Identifier: BUSL-1.1

package configs

import (
	"testing"

	"github.com/hashicorp/hcl/v2"

	"github.com/hashicorp/terraform/internal/experiments"
)

// TestEnabledMetaArgumentExperiment verifies "enabled" is gated behind an
// opt-in experiment: unavailable until activated, then available.
func TestEnabledMetaArgumentExperiment(t *testing.T) {
	t.Run("not activated", func(t *testing.T) {
		_, diags := testModuleFromDirWithExperiments("testdata/enabled-meta-argument/not-active")
		if !diags.HasErrors() {
			t.Fatal("expected an error, but there was none")
		}

		var found int
		for _, diag := range diags {
			if diag.Summary == `The "enabled" meta-argument is experimental` {
				found++
			}
		}
		// One each for the resource, data, ephemeral, and module call blocks.
		if found != 4 {
			t.Fatalf("expected 4 \"enabled\" experiment errors, got %d\n%s", found, diags.Error())
		}
	})

	t.Run("activated", func(t *testing.T) {
		mod, diags := testModuleFromDirWithExperiments("testdata/enabled-meta-argument/active")
		for _, diag := range diags {
			if diag.Summary == `The "enabled" meta-argument is experimental` {
				t.Errorf("unexpected experiment-not-active error: %s", diag)
			}
			if diag.Severity == hcl.DiagError {
				t.Errorf("unexpected error diagnostic: %s", diag)
			}
		}

		if !mod.ActiveExperiments.Has(experiments.EnabledMetaArgument) {
			t.Error("module does not indicate enabled_meta_argument experiment as active")
		}

		rc := mod.ManagedResources["test.foo"]
		if rc == nil || rc.Enabled == nil {
			t.Fatal("resource test.foo was not decoded with an Enabled expression")
		}

		dc := mod.DataResources["data.test.foo"]
		if dc == nil || dc.Enabled == nil {
			t.Fatal("data.test.foo was not decoded with an Enabled expression")
		}

		ec := mod.EphemeralResources["ephemeral.test.foo"]
		if ec == nil || ec.Enabled == nil {
			t.Fatal("ephemeral.test.foo was not decoded with an Enabled expression")
		}

		mc := mod.ModuleCalls["bar"]
		if mc == nil || mc.Enabled == nil {
			t.Fatal("module call \"bar\" was not decoded with an Enabled expression")
		}
	})
}
