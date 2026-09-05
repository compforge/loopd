package crd_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	api "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	"sigs.k8s.io/yaml"
)

// Generated YAML must pass Kubernetes's admission schema and CEL cost checks;
// controller-gen success alone does not establish installability.
func TestGeneratedCRDAdmission(t *testing.T) {
	paths, err := filepath.Glob("bases/*.yaml")
	if err != nil || len(paths) != 4 {
		t.Fatalf("CRDs=%v %v", paths, err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var external v1.CustomResourceDefinition
			if err := yaml.Unmarshal(data, &external); err != nil {
				t.Fatal(err)
			}
			v1.SetDefaults_CustomResourceDefinition(&external)
			var internal api.CustomResourceDefinition
			if err := v1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(&external, &internal, nil); err != nil {
				t.Fatal(err)
			}
			for _, version := range internal.Spec.Versions {
				if version.Storage {
					internal.Status.StoredVersions = []string{version.Name}
				}
			}
			if errs := validation.ValidateCustomResourceDefinition(context.Background(), &internal); len(errs) > 0 {
				t.Fatal(errs.ToAggregate())
			}
		})
	}
}
