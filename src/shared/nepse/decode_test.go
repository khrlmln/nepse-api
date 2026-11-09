package nepse

import (
	"strings"
	"testing"
)

func TestDecodeJSONRequiresExactlyOneTopLevelValue(t *testing.T) {
	for _, input := range []string{
		`{"price": 12.5}`,
		"{\"items\": []} \n\t",
	} {
		if _, err := decodeJSON([]byte(input)); err != nil {
			t.Errorf("valid JSON %q rejected: %v", input, err)
		}
	}

	for _, input := range []string{
		`{} {}`,
		`{} null`,
		`{} trailing`,
	} {
		if _, err := decodeJSON([]byte(input)); err == nil || !strings.Contains(err.Error(), "trailing data") {
			t.Errorf("trailing content %q was not rejected as an upstream response error: %v", input, err)
		}
	}
}
