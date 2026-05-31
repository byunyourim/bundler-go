package userop

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/byunyourim/stablecoin-bundler/internal/chain"
	"github.com/byunyourim/stablecoin-bundler/internal/core"
)

func parseChainIDFromParamsJSON(t *testing.T, arr string) int64 {
	t.Helper()
	var params []json.RawMessage
	if err := json.Unmarshal([]byte(arr), &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	return parseChainIDFromParams(params)
}

func testHandler(envMap map[string]string) *Handler {
	reg := chain.NewRegistryWithLookup(func(k string) string { return envMap[k] })
	return &Handler{deps: &core.Deps{Reg: reg}}
}

func TestRPC_ChainId(t *testing.T) {
	h := testHandler(map[string]string{"ENTRYPOINT_ADDRESS": "0xabc", "RPC_URL": "http://x"})
	resp := h.handleRPC(context.Background(), []byte(`{"id":1,"method":"eth_chainId"}`), 56357)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	// 56357 = 0xdc25
	if resp.Result != "0xdc25" {
		t.Fatalf("eth_chainId = %v, want 0xdc25", resp.Result)
	}
}

func TestRPC_SupportedEntryPoints(t *testing.T) {
	h := testHandler(map[string]string{"ENTRYPOINT_ADDRESS": "0xEntry", "RPC_URL": "http://x"})
	resp := h.handleRPC(context.Background(), []byte(`{"id":1,"method":"eth_supportedEntryPoints"}`), 1)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	got, ok := resp.Result.([]string)
	if !ok || len(got) != 1 || got[0] != "0xEntry" {
		t.Fatalf("supportedEntryPoints = %v", resp.Result)
	}
}

func TestRPC_MethodNotFound(t *testing.T) {
	h := testHandler(map[string]string{"ENTRYPOINT_ADDRESS": "0xabc", "RPC_URL": "http://x"})
	resp := h.handleRPC(context.Background(), []byte(`{"id":1,"method":"eth_foo"}`), 1)
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("want -32601 Method not found, got %+v", resp.Error)
	}
}

func TestRPC_ChainIdRequired(t *testing.T) {
	// path/params/DEFAULT 모두 없음 → -32602.
	h := testHandler(map[string]string{"ENTRYPOINT_ADDRESS": "0xabc", "RPC_URL": "http://x"})
	resp := h.handleRPC(context.Background(), []byte(`{"id":1,"method":"eth_chainId"}`), 0)
	if resp.Error == nil || resp.Error.Code != -32602 {
		t.Fatalf("want -32602 chainId required, got %+v", resp.Error)
	}
}

func TestRPC_MissingUserOp(t *testing.T) {
	h := testHandler(map[string]string{"ENTRYPOINT_ADDRESS": "0xabc", "RPC_URL": "http://x"})
	resp := h.handleRPC(context.Background(), []byte(`{"id":1,"method":"eth_sendUserOperation","params":[]}`), 1)
	if resp.Error == nil || resp.Error.Code != -32602 {
		t.Fatalf("want -32602 Missing userOp, got %+v", resp.Error)
	}
}

func TestRPC_InvalidRequest(t *testing.T) {
	h := testHandler(map[string]string{"ENTRYPOINT_ADDRESS": "0xabc", "RPC_URL": "http://x"})
	resp := h.handleRPC(context.Background(), []byte(`{"id":1}`), 1)
	if resp.Error == nil || resp.Error.Code != -32600 {
		t.Fatalf("want -32600 Invalid Request, got %+v", resp.Error)
	}
}

func TestParseChainIDFromParams(t *testing.T) {
	// params[2] = number
	resp := parseChainIDFromParamsJSON(t, `[{},"0xep",43113]`)
	if resp != 43113 {
		t.Fatalf("number param chainId = %d, want 43113", resp)
	}
	// params[2] = hex string
	resp = parseChainIDFromParamsJSON(t, `[{},"0xep","0xa869"]`)
	if resp != 43113 {
		t.Fatalf("hex param chainId = %d, want 43113", resp)
	}
}
