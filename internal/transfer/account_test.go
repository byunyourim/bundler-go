package transfer

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// 서명한 키 주소가 digest에서 ecrecover로 복원되어야 한다(온체인 ecrecover와 동일 경로).
func TestGetDigestSign65_Recover(t *testing.T) {
	priv, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	want := crypto.PubkeyToAddress(priv.PublicKey)

	account := common.HexToAddress("0x1111111111111111111111111111111111111111")
	to := common.HexToAddress("0x2222222222222222222222222222222222222222")
	data := []byte("0x")

	digest, err := getDigest(account, big.NewInt(7), to, big.NewInt(1000), data)
	if err != nil {
		t.Fatalf("getDigest: %v", err)
	}
	sig, err := sign65(digest, priv)
	if err != nil {
		t.Fatalf("sign65: %v", err)
	}
	if len(sig) != 65 {
		t.Fatalf("sig len = %d, want 65", len(sig))
	}
	if sig[64] != 27 && sig[64] != 28 {
		t.Fatalf("v = %d, want 27/28", sig[64])
	}

	// 복원: v를 0/1로 되돌려 ecrecover.
	rec := make([]byte, 65)
	copy(rec, sig)
	rec[64] -= 27
	pub, err := crypto.SigToPub(digest, rec)
	if err != nil {
		t.Fatalf("SigToPub: %v", err)
	}
	got := crypto.PubkeyToAddress(*pub)
	if got != want {
		t.Fatalf("recovered %s, want %s", got.Hex(), want.Hex())
	}
}

func TestGetDigest_Deterministic(t *testing.T) {
	account := common.HexToAddress("0xabc0000000000000000000000000000000000001")
	to := common.HexToAddress("0xabc0000000000000000000000000000000000002")
	d1, _ := getDigest(account, big.NewInt(1), to, big.NewInt(5), []byte{0x01, 0x02})
	d2, _ := getDigest(account, big.NewInt(1), to, big.NewInt(5), []byte{0x01, 0x02})
	d3, _ := getDigest(account, big.NewInt(2), to, big.NewInt(5), []byte{0x01, 0x02})
	if string(d1) != string(d2) {
		t.Fatal("digest not deterministic")
	}
	if string(d1) == string(d3) {
		t.Fatal("digest must change with nonce")
	}
}
