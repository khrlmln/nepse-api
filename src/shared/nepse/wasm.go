package nepse

import (
	"context"
	"fmt"
	"math"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

type wasmTokenParser struct {
	rt  wazero.Runtime
	mod wazero.CompiledModule
}

func newWASMTokenParser(ctx context.Context, b []byte) (*wasmTokenParser, error) {
	rt := wazero.NewRuntime(ctx)
	mod, err := rt.CompileModule(ctx, b)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("compile css.wasm: %w", err)
	}
	return &wasmTokenParser{rt: rt, mod: mod}, nil
}
func (p *wasmTokenParser) Close(ctx context.Context) error { return p.rt.Close(ctx) }

// call invokes one i32(i32 x5) export of css.wasm. Every export takes and returns a signed 32-bit integer, so arguments are range-checked and encoded as i32 (a sign-extended 64-bit value would leave garbage in the upper bits) and the result is decoded as a signed i32.
func (p *wasmTokenParser) call(ctx context.Context, name string, a [5]int) (int, error) {
	var params [5]uint64
	for i, v := range a {
		if v < math.MinInt32 || v > math.MaxInt32 {
			return 0, fmt.Errorf("salt %d does not fit in a 32-bit integer", v)
		}
		params[i] = api.EncodeI32(int32(v))
	}
	m, err := p.rt.InstantiateModule(ctx, p.mod, wazero.NewModuleConfig())
	if err != nil {
		return 0, err
	}
	defer m.Close(ctx)
	fn := m.ExportedFunction(name)
	if fn == nil {
		return 0, fmt.Errorf("css.wasm export %q not found", name)
	}
	res, err := fn.Call(ctx, params[:]...)
	if err != nil {
		return 0, err
	}
	if len(res) != 1 {
		return 0, fmt.Errorf("css.wasm %s returned %d values", name, len(res))
	}
	return int(api.DecodeI32(res[0])), nil
}

func removeAtRanges(s string, idx [5]int) (string, error) {
	if len(s) < 5 {
		return "", fmt.Errorf("token too short")
	}
	last := -1
	for _, i := range idx {
		if i < 0 || i >= len(s) {
			return "", fmt.Errorf("token index %d out of range", i)
		}
		if i <= last {
			return "", fmt.Errorf("token indices must be strictly increasing")
		}
		last = i
	}
	out := make([]byte, 0, len(s)-5)
	last = 0
	for _, i := range idx {
		out = append(out, s[last:i]...)
		last = i + 1
	}
	out = append(out, s[last:]...)
	return string(out), nil
}

func (p *wasmTokenParser) parse(ctx context.Context, a AuthResponse) (string, string, error) {
	accessArgs := [5]int{a.Salt1, a.Salt2, a.Salt3, a.Salt4, a.Salt5}
	permArgs := [5]int{a.Salt1, a.Salt2, a.Salt4, a.Salt3, a.Salt5}
	names := []string{"cdx", "rdx", "bdx", "ndx", "mdx"}
	var ai, ri [5]int
	for i, name := range names {
		args := permArgs
		if i == 0 {
			args = accessArgs
		}
		v, err := p.call(ctx, name, args)
		if err != nil {
			return "", "", err
		}
		ai[i] = v
	}
	refreshSets := [5][5]int{
		{a.Salt2, a.Salt1, a.Salt3, a.Salt5, a.Salt4},
		{a.Salt2, a.Salt1, a.Salt3, a.Salt4, a.Salt5},
		{a.Salt2, a.Salt1, a.Salt4, a.Salt3, a.Salt5},
		{a.Salt2, a.Salt1, a.Salt4, a.Salt3, a.Salt5},
		{a.Salt2, a.Salt1, a.Salt4, a.Salt3, a.Salt5},
	}
	for i, name := range names {
		v, err := p.call(ctx, name, refreshSets[i])
		if err != nil {
			return "", "", err
		}
		ri[i] = v
	}
	acc, err := removeAtRanges(a.AccessToken, ai)
	if err != nil {
		return "", "", fmt.Errorf("access token: %w", err)
	}
	ref, err := removeAtRanges(a.RefreshToken, ri)
	if err != nil {
		return "", "", fmt.Errorf("refresh token: %w", err)
	}
	return acc, ref, nil
}
