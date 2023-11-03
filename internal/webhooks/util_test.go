package webhooks

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

var ctx = ctrl.SetupSignalHandler()

// customDefaulterValidator interface is for objects that define both custom defaulting
// and custom validating webhooks.
type customDefaulterValidator interface {
	webhook.CustomDefaulter
	webhook.CustomValidator
}
