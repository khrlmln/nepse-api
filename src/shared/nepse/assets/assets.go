// Package assets embeds the static files the NEPSE client needs.
package assets

import _ "embed"

//go:embed css.wasm
var cssWASM []byte

//go:embed API_ENDPOINTS.json
var endpointsJSON []byte

//go:embed PAYLOAD_SEEDS.json
var payloadSeedsJSON []byte

//go:embed HEADERS.json
var headersJSON []byte

func CSSWASM() []byte          { return cssWASM }
func EndpointsJSON() []byte    { return endpointsJSON }
func PayloadSeedsJSON() []byte { return payloadSeedsJSON }
func HeadersJSON() []byte      { return headersJSON }
