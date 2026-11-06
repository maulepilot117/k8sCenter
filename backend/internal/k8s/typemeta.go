package k8s

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
)

// StampTypeMeta fills obj's apiVersion and kind from client-go's scheme when
// both are empty, and leaves an object that already carries them untouched.
//
// Typed objects decoded by client-go, from an informer cache or a typed
// clientset call, arrive with an empty TypeMeta. Serving one as-is yields a
// manifest with no apiVersion or kind, which cannot be re-applied. An object
// whose type the scheme does not know is left as it is.
//
// It mutates obj in place, so never pass an object shared with an informer
// cache: deep-copy it first.
func StampTypeMeta(obj runtime.Object) {
	if obj == nil || !obj.GetObjectKind().GroupVersionKind().Empty() {
		return
	}
	gvks, _, err := scheme.Scheme.ObjectKinds(obj)
	if err != nil || len(gvks) == 0 {
		return
	}
	obj.GetObjectKind().SetGroupVersionKind(gvks[0])
}
