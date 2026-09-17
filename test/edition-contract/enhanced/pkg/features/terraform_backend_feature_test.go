package features

import "testing"

func TestFeaturesAdvertiseImplementedTerraformBackend(t *testing.T) {
	if !GetFeatures().TerraformBackend {
		t.Fatal("implemented Terraform backend must be advertised to the selected Enhanced UI")
	}
}
