package wizard

import (
	"strings"
	"testing"
)

func validExternalSecretInput() ExternalSecretInput {
	return ExternalSecretInput{
		Name:             "my-app-config",
		Namespace:        "apps",
		StoreRef:         ExternalSecretStoreRefInput{Name: "vault-store", Kind: "SecretStore"},
		RefreshInterval:  "1h",
		TargetSecretName: "my-app-config",
		Data: []ExternalSecretDataItemInput{
			{SecretKey: "DB_PASSWORD", RemoteRef: ExternalSecretRemoteRefInput{Key: "secret/data/myapp", Property: "db_password"}},
		},
	}
}

func TestExternalSecretValidate_Valid(t *testing.T) {
	e := validExternalSecretInput()
	if errs := e.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestExternalSecretValidate_InvalidName(t *testing.T) {
	cases := []string{"", "MyApp", "-bad", "bad-", "has space", strings.Repeat("a", 64)}
	for _, n := range cases {
		e := validExternalSecretInput()
		e.Name = n
		errs := e.Validate()
		if !hasField(errs, "name") {
			t.Errorf("expected name error for %q, got %v", n, errs)
		}
	}
}

func TestExternalSecretValidate_MissingNamespace(t *testing.T) {
	e := validExternalSecretInput()
	e.Namespace = ""
	if !hasField(e.Validate(), "namespace") {
		t.Error("expected namespace error")
	}
}

func TestExternalSecretValidate_StoreRefKind(t *testing.T) {
	cases := []string{"", "BadKind", "secretstore"}
	for _, k := range cases {
		e := validExternalSecretInput()
		e.StoreRef.Kind = k
		if !hasField(e.Validate(), "storeRef.kind") {
			t.Errorf("expected storeRef.kind error for %q", k)
		}
	}
}

func TestExternalSecretValidate_ClusterStoreKind_Valid(t *testing.T) {
	e := validExternalSecretInput()
	e.StoreRef.Kind = "ClusterSecretStore"
	if errs := e.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestExternalSecretValidate_StoreRefName(t *testing.T) {
	e := validExternalSecretInput()
	e.StoreRef.Name = ""
	if !hasField(e.Validate(), "storeRef.name") {
		t.Error("expected storeRef.name error")
	}
	e.StoreRef.Name = "BadName"
	if !hasField(e.Validate(), "storeRef.name") {
		t.Error("expected storeRef.name DNS error")
	}
}

func TestExternalSecretValidate_TargetSecretName(t *testing.T) {
	e := validExternalSecretInput()
	e.TargetSecretName = ""
	if !hasField(e.Validate(), "targetSecretName") {
		t.Error("expected targetSecretName required")
	}
	e.TargetSecretName = "BadName"
	if !hasField(e.Validate(), "targetSecretName") {
		t.Error("expected targetSecretName DNS error")
	}
}

func TestExternalSecretValidate_BadRefreshInterval(t *testing.T) {
	e := validExternalSecretInput()
	e.RefreshInterval = "1z"
	if !hasField(e.Validate(), "refreshInterval") {
		t.Error("expected refreshInterval error")
	}
	e.RefreshInterval = "-5s"
	if !hasField(e.Validate(), "refreshInterval") {
		t.Error("expected negative refreshInterval error")
	}
}

func TestExternalSecretValidate_RefreshIntervalZero(t *testing.T) {
	e := validExternalSecretInput()
	e.RefreshInterval = "0"
	if errs := e.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors for zero refresh, got %v", errs)
	}
}

func TestExternalSecretValidate_NoDataAndNoDataFrom(t *testing.T) {
	e := validExternalSecretInput()
	e.Data = nil
	if !hasField(e.Validate(), "data") {
		t.Error("expected error when both data and dataFrom are empty")
	}
}

func TestExternalSecretValidate_DataFromOnly_Extract(t *testing.T) {
	e := validExternalSecretInput()
	e.Data = nil
	e.DataFrom = []ExternalSecretDataFromInput{
		{Extract: &ExternalSecretRemoteRefInput{Key: "secret/data/myapp"}},
	}
	if errs := e.Validate(); len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestExternalSecretValidate_DataFromBothExtractAndFind(t *testing.T) {
	e := validExternalSecretInput()
	e.Data = nil
	e.DataFrom = []ExternalSecretDataFromInput{{
		Extract: &ExternalSecretRemoteRefInput{Key: "k"},
		Find:    &ExternalSecretFindInput{Name: &ExternalSecretFindBy{RegExp: "^x$"}},
	}}
	errs := e.Validate()
	if !hasField(errs, "dataFrom[0]") {
		t.Errorf("expected mutual-exclusion error, got %v", errs)
	}
}

func TestExternalSecretValidate_DataFromExtractMissingKey(t *testing.T) {
	e := validExternalSecretInput()
	e.Data = nil
	e.DataFrom = []ExternalSecretDataFromInput{{Extract: &ExternalSecretRemoteRefInput{}}}
	if !hasField(e.Validate(), "dataFrom[0].extract.key") {
		t.Error("expected extract.key error")
	}
}

func TestExternalSecretValidate_DataFromFindMissingRegex(t *testing.T) {
	e := validExternalSecretInput()
	e.Data = nil
	e.DataFrom = []ExternalSecretDataFromInput{{Find: &ExternalSecretFindInput{}}}
	if !hasField(e.Validate(), "dataFrom[0].find.name.regexp") {
		t.Error("expected find regex required error")
	}
}

func TestExternalSecretValidate_DataItemMissingFields(t *testing.T) {
	e := validExternalSecretInput()
	e.Data = []ExternalSecretDataItemInput{
		{},                                         // missing both
		{SecretKey: "ok"},                          // missing remoteRef.key
		{RemoteRef: ExternalSecretRemoteRefInput{Key: "k"}}, // missing secretKey
	}
	errs := e.Validate()
	// At least three field errors expected.
	if len(errs) < 3 {
		t.Errorf("expected at least 3 errors, got %d: %v", len(errs), errs)
	}
}

func TestExternalSecretValidate_DuplicateSecretKey(t *testing.T) {
	e := validExternalSecretInput()
	e.Data = []ExternalSecretDataItemInput{
		{SecretKey: "DUP", RemoteRef: ExternalSecretRemoteRefInput{Key: "k1"}},
		{SecretKey: "DUP", RemoteRef: ExternalSecretRemoteRefInput{Key: "k2"}},
	}
	if !hasField(e.Validate(), "data[1].secretKey") {
		t.Error("expected duplicate secretKey error")
	}
}

func TestExternalSecretToYAML_BasicShape(t *testing.T) {
	e := validExternalSecretInput()
	y, err := e.ToYAML()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"apiVersion: external-secrets.io/v1",
		"kind: ExternalSecret",
		"name: my-app-config",
		"namespace: apps",
		"refreshInterval: 1h",
		"secretStoreRef:",
		"kind: SecretStore",
		"target:",
		"data:",
		"DB_PASSWORD",
		"property: db_password",
	} {
		if !strings.Contains(y, want) {
			t.Errorf("expected YAML to contain %q\n%s", want, y)
		}
	}
}

func TestExternalSecretToYAML_DataFromExtract(t *testing.T) {
	e := validExternalSecretInput()
	e.Data = nil
	e.DataFrom = []ExternalSecretDataFromInput{
		{Extract: &ExternalSecretRemoteRefInput{Key: "secret/data/myapp"}},
	}
	y, err := e.ToYAML()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(y, "dataFrom:") || !strings.Contains(y, "extract:") {
		t.Errorf("expected dataFrom + extract, got\n%s", y)
	}
}

func TestExternalSecretToYAML_OmitsZeroFields(t *testing.T) {
	e := validExternalSecretInput()
	e.RefreshInterval = ""
	y, err := e.ToYAML()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(y, "refreshInterval") {
		t.Errorf("expected refreshInterval omitted, got\n%s", y)
	}
}

func hasField(errs []FieldError, field string) bool {
	for _, e := range errs {
		if e.Field == field {
			return true
		}
	}
	return false
}
