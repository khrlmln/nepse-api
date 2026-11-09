package assets

import (
	"encoding/json"
	"testing"
)

func TestEmbeddedRuntimeAssetsArePresentAndValid(t *testing.T) {
	if len(CSSWASM()) == 0 {
		t.Fatal("embedded css.wasm is empty")
	}
	for name, data := range map[string][]byte{
		"API_ENDPOINTS.json": EndpointsJSON(),
		"PAYLOAD_SEEDS.json": PayloadSeedsJSON(),
		"HEADERS.json":       HeadersJSON(),
	} {
		t.Run(name, func(t *testing.T) {
			if len(data) == 0 {
				t.Fatal("embedded asset is empty")
			}
			var value any
			if err := json.Unmarshal(data, &value); err != nil {
				t.Fatalf("embedded asset is not valid JSON: %v", err)
			}
		})
	}
}
