package helps

import (
	"bytes"
	"encoding/json"
)

// NormalizeDevinGeminiToolSchema moves string enums containing "" to description hints.
// Devin's Gemini backend rejects the empty enum member; tool execution remains caller-owned.
func NormalizeDevinGeminiToolSchema(parameters []byte) []byte {
	if !json.Valid(parameters) {
		return parameters
	}
	var schema any
	decoder := json.NewDecoder(bytes.NewReader(parameters))
	decoder.UseNumber()
	// shortcut: Preserve referenced schemas until their usage contexts can be normalized safely.
	if errDecode := decoder.Decode(&schema); errDecode != nil || hasDevinSchemaReference(schema) || !normalizeDevinGeminiSchema(schema) {
		return parameters
	}
	normalized, errMarshal := json.Marshal(schema)
	if errMarshal != nil {
		return parameters
	}
	return normalized
}

func hasDevinSchemaReference(value any) bool {
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			if key == "$ref" || key == "$dynamicRef" || key == "$recursiveRef" || hasDevinSchemaReference(child) {
				return true
			}
		}
	case []any:
		for _, child := range node {
			if hasDevinSchemaReference(child) {
				return true
			}
		}
	}
	return false
}

func normalizeDevinGeminiSchema(value any) bool {
	node, ok := value.(map[string]any)
	if !ok {
		return false
	}
	changed := false
	if node["type"] == "string" {
		values, _ := node["enum"].([]any)
		hasEmpty, stringsOnly := false, true
		for _, value := range values {
			text, isString := value.(string)
			stringsOnly = stringsOnly && isString
			hasEmpty = hasEmpty || (isString && text == "")
		}
		if hasEmpty && stringsOnly {
			allowed, _ := json.Marshal(values)
			description, _ := node["description"].(string)
			if description != "" {
				description += "\n"
			}
			node["description"] = description + "Allowed values: " + string(allowed) + "."
			delete(node, "enum")
			changed = true
		}
	}
	// shortcut: Preserve not/if/oneOf/contains; extend only with an equivalent upstream encoding.
	for key, child := range node {
		switch key {
		case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas":
			children, _ := child.(map[string]any)
			for _, schema := range children {
				changed = normalizeDevinGeminiSchema(schema) || changed
			}
		case "items", "prefixItems", "allOf", "anyOf":
			if children, isArray := child.([]any); isArray {
				for _, schema := range children {
					changed = normalizeDevinGeminiSchema(schema) || changed
				}
			} else {
				changed = normalizeDevinGeminiSchema(child) || changed
			}
		case "additionalProperties", "unevaluatedProperties", "propertyNames", "then", "else":
			changed = normalizeDevinGeminiSchema(child) || changed
		}
	}
	return changed
}
