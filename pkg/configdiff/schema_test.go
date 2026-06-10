package configdiff

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedAnalysisMatchesV1Schema(t *testing.T) {
	outDir := filepath.Join(t.TempDir(), "report")
	if _, err := Explain(Options{
		BeforePath: filepath.Join("..", "..", "testdata", "sample-before.cfg"),
		AfterPath:  filepath.Join("..", "..", "testdata", "sample-after.cfg"),
		Vendor:     "auto",
		OutDir:     outDir,
	}); err != nil {
		t.Fatalf("Explain returned error: %v", err)
	}

	schemaBytes, err := os.ReadFile(filepath.Join("..", "..", "schema", "diff-analysis-v1.schema.json"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	jsonBytes, err := os.ReadFile(filepath.Join(outDir, "diff-analysis.json"))
	if err != nil {
		t.Fatalf("read generated analysis: %v", err)
	}

	var schema map[string]any
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	var document any
	if err := json.Unmarshal(jsonBytes, &document); err != nil {
		t.Fatalf("analysis is not valid JSON: %v", err)
	}
	if err := validateSchemaNode(schema, schema, document, "$"); err != nil {
		t.Fatal(err)
	}
}

func validateSchemaNode(root, schema map[string]any, value any, path string) error {
	if ref, ok := schema["$ref"].(string); ok {
		resolved, err := resolveSchemaRef(root, ref)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		return validateSchemaNode(root, resolved, value, path)
	}

	if enums, ok := schema["enum"].([]any); ok {
		if !containsJSONValue(enums, value) {
			return fmt.Errorf("%s: value %v is not in enum %v", path, value, enums)
		}
	}

	schemaType, _ := schema["type"].(string)
	switch schemaType {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: expected object, got %T", path, value)
		}
		if required, ok := schema["required"].([]any); ok {
			for _, item := range required {
				key, ok := item.(string)
				if !ok {
					return fmt.Errorf("%s: required entry is not a string", path)
				}
				if _, exists := object[key]; !exists {
					return fmt.Errorf("%s: missing required key %q", path, key)
				}
			}
		}
		properties, _ := schema["properties"].(map[string]any)
		for key, rawPropertySchema := range properties {
			childValue, exists := object[key]
			if !exists {
				continue
			}
			propertySchema, ok := rawPropertySchema.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.%s: property schema is not an object", path, key)
			}
			if err := validateSchemaNode(root, propertySchema, childValue, path+"."+key); err != nil {
				return err
			}
		}
	case "array":
		array, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s: expected array, got %T", path, value)
		}
		itemSchema, _ := schema["items"].(map[string]any)
		if itemSchema != nil {
			for i, item := range array {
				if err := validateSchemaNode(root, itemSchema, item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s: expected string, got %T", path, value)
		}
	case "number":
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("%s: expected number, got %T", path, value)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s: expected boolean, got %T", path, value)
		}
	}
	return nil
}

func resolveSchemaRef(root map[string]any, ref string) (map[string]any, error) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("unsupported ref %q", ref)
	}
	var current any = root
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("ref %q traversed through non-object", ref)
		}
		current, ok = object[part]
		if !ok {
			return nil, fmt.Errorf("ref %q missing part %q", ref, part)
		}
	}
	schema, ok := current.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("ref %q did not resolve to schema object", ref)
	}
	return schema, nil
}

func containsJSONValue(values []any, want any) bool {
	for _, value := range values {
		if fmt.Sprint(value) == fmt.Sprint(want) {
			return true
		}
	}
	return false
}
