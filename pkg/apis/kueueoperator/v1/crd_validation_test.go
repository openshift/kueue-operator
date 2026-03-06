package v1

import (
	"context"
	"os"
	"strings"
	"testing"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsvalidation "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func TestCRDValidation(t *testing.T) {
	crdPath := "../../../../manifests/kueue.openshift.io_kueues.yaml"
	data, err := os.ReadFile(crdPath)
	if err != nil {
		t.Fatalf("failed to read CRD file: %v", err)
	}

	scheme := runtime.NewScheme()
	if err := apiextensionsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add v1 scheme: %v", err)
	}
	codec := serializer.NewCodecFactory(scheme)
	v1CRD := &apiextensionsv1.CustomResourceDefinition{}
	if _, _, err := codec.UniversalDeserializer().Decode(data, nil, v1CRD); err != nil {
		t.Fatalf("failed to decode CRD: %v", err)
	}

	internalScheme := runtime.NewScheme()
	if err := apiextensions.AddToScheme(internalScheme); err != nil {
		t.Fatalf("failed to add internal scheme: %v", err)
	}
	if err := apiextensionsv1.AddToScheme(internalScheme); err != nil {
		t.Fatalf("failed to add v1 to internal scheme: %v", err)
	}
	internalCRD := &apiextensions.CustomResourceDefinition{}
	if err := internalScheme.Convert(v1CRD, internalCRD, nil); err != nil {
		t.Fatalf("failed to convert CRD to internal: %v", err)
	}

	errs := apiextensionsvalidation.ValidateCustomResourceDefinition(context.Background(), internalCRD)

	var relevant field.ErrorList
	for _, e := range errs {
		if e.Field == "status" || strings.HasPrefix(e.Field, "status.") {
			continue
		}
		relevant = append(relevant, e)
	}

	if len(relevant) > 0 {
		for _, e := range relevant {
			t.Errorf("CRD validation error: %s: %s", e.Field, e.Detail)
		}
		t.Fatalf("CRD validation failed with %d error(s)", len(relevant))
	}
}
