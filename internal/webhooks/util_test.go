package webhooks

import (
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var ctx = ctrl.SetupSignalHandler()

// customDefaulterValidator interface is for objects that define both custom defaulting
// and custom validating webhooks.
type customDefaulterValidator[T runtime.Object] interface {
	admission.Defaulter[T]
	admission.Validator[T]
}
