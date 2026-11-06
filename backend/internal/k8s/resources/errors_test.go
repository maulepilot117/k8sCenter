package resources

import (
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestIsNoPreviousContainer(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "api server refusal for a container that never restarted",
			err:  apierrors.NewBadRequest(`previous terminated container "app" in pod "web-0" not found`),
			want: true,
		},
		{
			name: "other bad request",
			err:  apierrors.NewBadRequest(`container "nope" is not valid for pod "web-0"`),
			want: false,
		},
		{
			name: "not found with the same words is not a 400",
			err: apierrors.NewNotFound(schema.GroupResource{Resource: "pods"},
				"previous terminated container"),
			want: false,
		},
		{
			name: "plain error",
			err:  errors.New("previous terminated container gone"),
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsNoPreviousContainer(tc.err); got != tc.want {
				t.Fatalf("IsNoPreviousContainer() = %v, want %v", got, tc.want)
			}
		})
	}
}
