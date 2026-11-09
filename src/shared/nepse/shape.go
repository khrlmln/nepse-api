package nepse

import (
	"fmt"

	apierrors "nepse-api/src/shared/errors"
)

// Upstream data is validated at the boundary so a changed NEPSE schema produces UPSTREAM_INVALID_RESPONSE instead of a confusing downstream failure. ExpectArray asserts v is a JSON array.
func ExpectArray(v any, what string) ([]any, error) {
	items, ok := v.([]any)
	if !ok {
		return nil, apierrors.UpstreamInvalidResponse(fmt.Errorf("%s: expected array, got %T", what, v), "")
	}
	return items, nil
}

// ExpectObject asserts v is a JSON object.
func ExpectObject(v any, what string) (map[string]any, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, apierrors.UpstreamInvalidResponse(fmt.Errorf("%s: expected object, got %T", what, v), "")
	}
	return m, nil
}

// ExpectObjects asserts v is a JSON array whose elements are all objects.
func ExpectObjects(v any, what string) ([]map[string]any, error) {
	items, err := ExpectArray(v, what)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(items))
	for i, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, apierrors.UpstreamInvalidResponse(fmt.Errorf("%s[%d]: expected object, got %T", what, i, item), "")
		}
		out = append(out, m)
	}
	return out, nil
}

// Str returns m[key] when it is a string and "" otherwise.
func Str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// ExpectCollection asserts v is a JSON array or object (upstream shapes that are documented only as "a collection").
func ExpectCollection(v any, what string) (any, error) {
	switch v.(type) {
	case []any, map[string]any:
		return v, nil
	default:
		return nil, apierrors.UpstreamInvalidResponse(fmt.Errorf("%s: expected array or object, got %T", what, v), "")
	}
}

// checkShape validates a decoded response against the shape an endpoint promises.
func checkShape(v any, kind shapeKind, what string) error {
	var err error
	switch kind {
	case shapeArray:
		_, err = ExpectArray(v, what)
	case shapeObjects:
		_, err = ExpectObjects(v, what)
	case shapeObject:
		_, err = ExpectObject(v, what)
	case shapeCollection:
		_, err = ExpectCollection(v, what)
	}
	return err
}
