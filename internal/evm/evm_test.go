package evm

import (
	"math/big"
	"testing"
)

func TestToBigInt(t *testing.T) {
	cases := []struct {
		in   any
		want int64
	}{
		{"0x10", 16},
		{"255", 255},
		{float64(42), 42},
		{big.NewInt(7), 7},
	}
	for _, c := range cases {
		got, err := ToBigInt(c.in)
		if err != nil {
			t.Fatalf("ToBigInt(%v): %v", c.in, err)
		}
		if got.Int64() != c.want {
			t.Fatalf("ToBigInt(%v) = %d, want %d", c.in, got.Int64(), c.want)
		}
	}
	if _, err := ToBigInt("nope"); err == nil {
		t.Fatal("expected error for invalid string")
	}
	if _, err := ToBigInt(3.5); err == nil {
		t.Fatal("expected error for non-integer float")
	}
}

func TestSaltToBytes32(t *testing.T) {
	// 0x + 64 hex → 그대로.
	hexSalt := "0x00000000000000000000000000000000000000000000000000000000000000ab"
	b := SaltToBytes32(hexSalt)
	if b[31] != 0xab {
		t.Fatalf("hex salt last byte = %x, want ab", b[31])
	}
	// 문자열 → keccak256, 결정론적.
	a := SaltToBytes32("account-proxy-1")
	c := SaltToBytes32("account-proxy-1")
	if a != c {
		t.Fatal("string salt not deterministic")
	}
	if a == SaltToBytes32("account-proxy-2") {
		t.Fatal("different strings should differ")
	}
}

func TestUint256JSON(t *testing.T) {
	var u Uint256
	if err := u.UnmarshalJSON([]byte(`"0xff"`)); err != nil {
		t.Fatal(err)
	}
	if u.Big().Int64() != 255 {
		t.Fatalf("hex string = %d, want 255", u.Big().Int64())
	}
	var n Uint256
	_ = n.UnmarshalJSON([]byte(`null`))
	if n.Big().Sign() != 0 {
		t.Fatal("null should yield 0")
	}
}
