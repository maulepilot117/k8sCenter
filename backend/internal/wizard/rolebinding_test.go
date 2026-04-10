package wizard

import (
	"testing"
)

func TestRoleBindingInputValidate(t *testing.T) {
	tests := []struct {
		name       string
		input      RoleBindingInput
		wantErrors int
		wantFields []string // fields that should have errors
	}{
		{
			name: "valid namespaced RoleBinding",
			input: RoleBindingInput{
				Name:      "my-binding",
				Namespace: "default",
				RoleRef:   RoleRefInput{Kind: "ClusterRole", Name: "view"},
				Subjects:  []SubjectInput{{Kind: "User", Name: "jane"}},
			},
			wantErrors: 0,
		},
		{
			name: "valid ClusterRoleBinding",
			input: RoleBindingInput{
				Name:         "my-crb",
				ClusterScope: true,
				RoleRef:      RoleRefInput{Kind: "ClusterRole", Name: "admin"},
				Subjects:     []SubjectInput{{Kind: "Group", Name: "devs"}},
			},
			wantErrors: 0,
		},
		{
			name: "valid with ServiceAccount subject",
			input: RoleBindingInput{
				Name:      "sa-binding",
				Namespace: "kube-system",
				RoleRef:   RoleRefInput{Kind: "Role", Name: "pod-reader"},
				Subjects:  []SubjectInput{{Kind: "ServiceAccount", Name: "my-sa", Namespace: "kube-system"}},
			},
			wantErrors: 0,
		},
		{
			name: "missing name",
			input: RoleBindingInput{
				Name:      "",
				Namespace: "default",
				RoleRef:   RoleRefInput{Kind: "ClusterRole", Name: "view"},
				Subjects:  []SubjectInput{{Kind: "User", Name: "jane"}},
			},
			wantErrors: 1,
			wantFields: []string{"name"},
		},
		{
			name: "invalid name (uppercase)",
			input: RoleBindingInput{
				Name:      "MyBinding",
				Namespace: "default",
				RoleRef:   RoleRefInput{Kind: "ClusterRole", Name: "view"},
				Subjects:  []SubjectInput{{Kind: "User", Name: "jane"}},
			},
			wantErrors: 1,
			wantFields: []string{"name"},
		},
		{
			name: "missing namespace for RoleBinding",
			input: RoleBindingInput{
				Name:     "my-binding",
				RoleRef:  RoleRefInput{Kind: "ClusterRole", Name: "view"},
				Subjects: []SubjectInput{{Kind: "User", Name: "jane"}},
			},
			wantErrors: 1,
			wantFields: []string{"namespace"},
		},
		{
			name: "ClusterRoleBinding referencing Role (invalid)",
			input: RoleBindingInput{
				Name:         "bad-crb",
				ClusterScope: true,
				RoleRef:      RoleRefInput{Kind: "Role", Name: "pod-reader"},
				Subjects:     []SubjectInput{{Kind: "User", Name: "jane"}},
			},
			wantErrors: 1,
			wantFields: []string{"roleRef.kind"},
		},
		{
			name: "missing roleRef name",
			input: RoleBindingInput{
				Name:      "my-binding",
				Namespace: "default",
				RoleRef:   RoleRefInput{Kind: "ClusterRole", Name: ""},
				Subjects:  []SubjectInput{{Kind: "User", Name: "jane"}},
			},
			wantErrors: 1,
			wantFields: []string{"roleRef.name"},
		},
		{
			name: "invalid roleRef kind",
			input: RoleBindingInput{
				Name:      "my-binding",
				Namespace: "default",
				RoleRef:   RoleRefInput{Kind: "InvalidKind", Name: "view"},
				Subjects:  []SubjectInput{{Kind: "User", Name: "jane"}},
			},
			wantErrors: 1,
			wantFields: []string{"roleRef.kind"},
		},
		{
			name: "no subjects",
			input: RoleBindingInput{
				Name:      "my-binding",
				Namespace: "default",
				RoleRef:   RoleRefInput{Kind: "ClusterRole", Name: "view"},
				Subjects:  []SubjectInput{},
			},
			wantErrors: 1,
			wantFields: []string{"subjects"},
		},
		{
			name: "subject with empty name",
			input: RoleBindingInput{
				Name:      "my-binding",
				Namespace: "default",
				RoleRef:   RoleRefInput{Kind: "ClusterRole", Name: "view"},
				Subjects:  []SubjectInput{{Kind: "User", Name: ""}},
			},
			wantErrors: 1,
			wantFields: []string{"subjects[0].name"},
		},
		{
			name: "invalid subject kind",
			input: RoleBindingInput{
				Name:      "my-binding",
				Namespace: "default",
				RoleRef:   RoleRefInput{Kind: "ClusterRole", Name: "view"},
				Subjects:  []SubjectInput{{Kind: "Pod", Name: "test"}},
			},
			wantErrors: 1,
			wantFields: []string{"subjects[0].kind"},
		},
		{
			name: "ServiceAccount missing namespace",
			input: RoleBindingInput{
				Name:      "my-binding",
				Namespace: "default",
				RoleRef:   RoleRefInput{Kind: "ClusterRole", Name: "view"},
				Subjects:  []SubjectInput{{Kind: "ServiceAccount", Name: "my-sa", Namespace: ""}},
			},
			wantErrors: 1,
			wantFields: []string{"subjects[0].namespace"},
		},
		{
			name: "too many subjects",
			input: func() RoleBindingInput {
				subjects := make([]SubjectInput, 51)
				for i := range subjects {
					subjects[i] = SubjectInput{Kind: "User", Name: "user"}
				}
				return RoleBindingInput{
					Name:      "my-binding",
					Namespace: "default",
					RoleRef:   RoleRefInput{Kind: "ClusterRole", Name: "view"},
					Subjects:  subjects,
				}
			}(),
			wantErrors: 1,
			wantFields: []string{"subjects"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := tt.input.Validate()
			if len(errs) != tt.wantErrors {
				t.Errorf("expected %d errors, got %d: %v", tt.wantErrors, len(errs), errs)
			}
			for _, wantField := range tt.wantFields {
				found := false
				for _, e := range errs {
					if e.Field == wantField {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error on field %q, not found in %v", wantField, errs)
				}
			}
		})
	}
}

func TestRoleBindingInputToRoleBinding(t *testing.T) {
	input := RoleBindingInput{
		Name:      "test-binding",
		Namespace: "default",
		RoleRef:   RoleRefInput{Kind: "ClusterRole", Name: "view"},
		Subjects: []SubjectInput{
			{Kind: "User", Name: "jane"},
			{Kind: "ServiceAccount", Name: "my-sa", Namespace: "kube-system"},
		},
	}

	rb := input.ToRoleBinding()

	if rb.Kind != "RoleBinding" {
		t.Errorf("expected Kind=RoleBinding, got %s", rb.Kind)
	}
	if rb.Name != "test-binding" {
		t.Errorf("expected Name=test-binding, got %s", rb.Name)
	}
	if rb.Namespace != "default" {
		t.Errorf("expected Namespace=default, got %s", rb.Namespace)
	}
	if rb.RoleRef.Kind != "ClusterRole" || rb.RoleRef.Name != "view" {
		t.Errorf("unexpected RoleRef: %+v", rb.RoleRef)
	}
	if rb.RoleRef.APIGroup != "rbac.authorization.k8s.io" {
		t.Errorf("expected RoleRef.APIGroup=rbac.authorization.k8s.io, got %s", rb.RoleRef.APIGroup)
	}
	if len(rb.Subjects) != 2 {
		t.Fatalf("expected 2 subjects, got %d", len(rb.Subjects))
	}
	// User subject should have rbac apiGroup
	if rb.Subjects[0].APIGroup != "rbac.authorization.k8s.io" {
		t.Errorf("User subject APIGroup should be rbac.authorization.k8s.io, got %s", rb.Subjects[0].APIGroup)
	}
	// ServiceAccount subject should have empty apiGroup and namespace set
	if rb.Subjects[1].APIGroup != "" {
		t.Errorf("ServiceAccount subject APIGroup should be empty, got %s", rb.Subjects[1].APIGroup)
	}
	if rb.Subjects[1].Namespace != "kube-system" {
		t.Errorf("ServiceAccount subject Namespace should be kube-system, got %s", rb.Subjects[1].Namespace)
	}
}

func TestRoleBindingInputToClusterRoleBinding(t *testing.T) {
	input := RoleBindingInput{
		Name:         "test-crb",
		ClusterScope: true,
		RoleRef:      RoleRefInput{Kind: "ClusterRole", Name: "admin"},
		Subjects:     []SubjectInput{{Kind: "Group", Name: "devs"}},
	}

	crb := input.ToClusterRoleBinding()

	if crb.Kind != "ClusterRoleBinding" {
		t.Errorf("expected Kind=ClusterRoleBinding, got %s", crb.Kind)
	}
	if crb.Name != "test-crb" {
		t.Errorf("expected Name=test-crb, got %s", crb.Name)
	}
	if crb.Namespace != "" {
		t.Errorf("ClusterRoleBinding should have no namespace, got %s", crb.Namespace)
	}
	if crb.Subjects[0].APIGroup != "rbac.authorization.k8s.io" {
		t.Errorf("Group subject APIGroup should be rbac.authorization.k8s.io, got %s", crb.Subjects[0].APIGroup)
	}
}
