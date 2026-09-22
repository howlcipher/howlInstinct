package receipt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// schemaPath is the published contract other Howl components vendor.
func schemaPath(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "schemas", "decision-receipt.schema.json")
}

func loadSchema(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(schemaPath(t))
	if err != nil {
		t.Fatalf("reading schema: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	return doc
}

// jsonFieldNames reflects over the struct's tags, which is what actually
// lands in a serialized receipt.
func jsonFieldNames(t reflect.Type) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		out = append(out, strings.Split(tag, ",")[0])
	}
	sort.Strings(out)
	return out
}

// The schema is a published cross-repo contract: siblings vendor a copy and
// validate against it. If the Go struct and the schema drift apart, those
// consumers start rejecting valid receipts or accepting invalid ones, and
// nothing in either repository would notice.
func TestSchemaMatchesTheStruct(t *testing.T) {
	doc := loadSchema(t)
	props, ok := doc["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema has no properties object")
	}

	inSchema := make(map[string]bool, len(props))
	for name := range props {
		inSchema[name] = true
	}

	inStruct := make(map[string]bool)
	for _, name := range jsonFieldNames(reflect.TypeOf(DecisionReceipt{})) {
		inStruct[name] = true
	}

	for name := range inStruct {
		if !inSchema[name] {
			t.Errorf("field %q exists on DecisionReceipt but is missing from the published schema", name)
		}
	}
	for name := range inSchema {
		if !inStruct[name] {
			t.Errorf("property %q exists in the published schema but not on DecisionReceipt", name)
		}
	}
}

// additionalProperties must stay false, or the schema stops being able to
// detect the drift the previous test guards against.
func TestSchemaRejectsUnknownProperties(t *testing.T) {
	doc := loadSchema(t)
	if additional, ok := doc["additionalProperties"].(bool); !ok || additional {
		t.Fatal("schema should set additionalProperties to false")
	}
}

// The identifier is versioned and echoed inside every instance, following the
// ecosystem convention. A mismatch would make a stored receipt claim
// conformance to a schema it was not built against.
func TestSchemaIdentifierMatchesTheConstant(t *testing.T) {
	doc := loadSchema(t)
	if got := doc["$id"]; got != Schema {
		t.Fatalf("schema $id = %v, want %q", got, Schema)
	}
	props := doc["properties"].(map[string]any)
	schemaProp, ok := props["schema"].(map[string]any)
	if !ok {
		t.Fatal("schema has no 'schema' property to pin the identifier inside instances")
	}
	if got := schemaProp["const"]; got != Schema {
		t.Fatalf("schema property const = %v, want %q", got, Schema)
	}
}

// Every field the schema marks required must actually be populated by a real
// receipt, or consumers validating against it would reject our own output.
func TestRequiredFieldsArePopulatedByRealReceipts(t *testing.T) {
	doc := loadSchema(t)
	required, ok := doc["required"].([]any)
	if !ok {
		t.Fatal("schema has no required list")
	}

	built, err := Build(sampleRequest(), sampleResponse(), sampleMeta(), fixedIDs())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	for _, r := range built {
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		var asMap map[string]any
		if err := json.Unmarshal(raw, &asMap); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		for _, field := range required {
			name := field.(string)
			if _, present := asMap[name]; !present {
				t.Errorf("receipt %q omits required field %q", r.QuestionID, name)
			}
		}
	}
}

// The absent-confidence rule has to hold at the schema level too: a noul
// receipt must not carry the property at all.
func TestSchemaDocumentsConfidenceAsAbsentNotZero(t *testing.T) {
	doc := loadSchema(t)
	props := doc["properties"].(map[string]any)
	conf := props["provider_confidence"].(map[string]any)

	desc, _ := conf["description"].(string)
	if !strings.Contains(strings.ToLower(desc), "absent") {
		t.Fatalf("provider_confidence description does not explain that it is absent rather than zero: %q", desc)
	}

	required := doc["required"].([]any)
	for _, f := range required {
		if f == "provider_confidence" {
			t.Fatal("provider_confidence must not be required: a noul has none")
		}
	}
}
