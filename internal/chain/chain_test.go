package chain

import "testing"

func reg(m map[string]string) *Registry {
	return NewRegistryWithLookup(func(k string) string { return m[k] })
}

func TestRPCURL_PerChainPriority(t *testing.T) {
	r := reg(map[string]string{"RPC_URL": "http://default", "RPC_URL_43113": "http://fuji"})
	if u, _ := r.RPCURL(43113); u != "http://fuji" {
		t.Fatalf("per-chain RPC = %q, want http://fuji", u)
	}
	if u, _ := r.RPCURL(1); u != "http://default" {
		t.Fatalf("fallback RPC = %q, want http://default", u)
	}
	if _, err := reg(map[string]string{}).RPCURL(1); err == nil {
		t.Fatal("expected error when no RPC configured")
	}
}

func TestResolveChainID(t *testing.T) {
	r := reg(map[string]string{"DEFAULT_CHAIN_ID": "56357"})
	if id, _ := r.ResolveChainID(43113); id != 43113 {
		t.Fatalf("candidate wins = %d", id)
	}
	if id, _ := r.ResolveChainID(0); id != 56357 {
		t.Fatalf("default used = %d, want 56357", id)
	}
	if _, err := reg(map[string]string{}).ResolveChainID(0); err == nil {
		t.Fatal("expected error when no chainId resolvable")
	}
}

func TestEOFSProxyPriority(t *testing.T) {
	r := reg(map[string]string{"EOFS_PROXY_ADDRESS": "0xdefault", "EOFS_PROXY_ADDRESS_56357": "0xkcp"})
	if v, _ := r.EOFSProxy(56357); v != "0xkcp" {
		t.Fatalf("per-chain EOFS = %q", v)
	}
	if v, _ := r.EOFSProxy(1); v != "0xdefault" {
		t.Fatalf("fallback EOFS = %q", v)
	}
}
