package k8s

import (
	"errors"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	intstr "k8s.io/apimachinery/pkg/util/intstr"
)

// ignoreMissing swallows NotFound so delete operations converge to "absent".
func ignoreMissing(err error) error {
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

var _ = errors.New

func intOrString(n int) intstr.IntOrString {
	return intstr.FromInt(n)
}
