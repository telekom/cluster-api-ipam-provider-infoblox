package applyconfiguration_test

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/applyconfiguration"
	"github.com/telekom/cluster-api-ipam-provider-infoblox/api/applyconfiguration/internal"
	api "github.com/telekom/cluster-api-ipam-provider-infoblox/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	smdschema "sigs.k8s.io/structured-merge-diff/v6/schema"
)

func TestForKindCoversRootKinds(t *testing.T) {
	scheme := runtime.NewScheme()
	NewWithT(t).Expect(api.AddToScheme(scheme)).To(Succeed())

	for gvk := range scheme.AllKnownTypes() {
		if gvk.GroupVersion() != api.GroupVersion || strings.HasSuffix(gvk.Kind, "List") ||
			strings.HasSuffix(gvk.Kind, "Options") || gvk.Kind == "WatchEvent" {
			continue
		}
		t.Run(gvk.Kind, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(applyconfiguration.ForKind(gvk)).NotTo(BeNil())

			object, err := scheme.New(gvk)
			g.Expect(err).NotTo(HaveOccurred())
			object.GetObjectKind().SetGroupVersionKind(gvk)
			_, err = applyconfiguration.NewTypeConverter(scheme).ObjectToTyped(object)
			g.Expect(err).NotTo(HaveOccurred())
		})
	}
}

func TestSchemaReferencesResolve(t *testing.T) {
	g := NewWithT(t)
	schema := &internal.Parser().Schema
	g.Expect(schema.Types).NotTo(BeEmpty())

	var missing []string
	check := func(owner string, ref smdschema.TypeRef) {
		if ref.NamedType == nil {
			return
		}
		if _, ok := schema.FindNamedType(*ref.NamedType); !ok {
			missing = append(missing, owner+" -> "+*ref.NamedType)
		}
	}
	for _, def := range schema.Types {
		if def.Map != nil {
			check(def.Name, def.Map.ElementType)
			for _, field := range def.Fields {
				check(def.Name+"."+field.Name, field.Type)
			}
		}
		if def.List != nil {
			check(def.Name, def.List.ElementType)
		}
	}
	g.Expect(missing).To(BeEmpty())
}
