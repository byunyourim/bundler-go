package txerror

import (
	"errors"
	"testing"
)

func TestNormalize_RevertCode(t *testing.T) {
	n := Normalize(errors.New("execution reverted: AC007"))
	if n.Code != "AC007" || n.Category != CategoryRevert || n.Retryable || n.Status != 422 {
		t.Errorf("got %+v", n)
	}
	if n.Message != "insufficient ERC20 balance" {
		t.Errorf("message=%q", n.Message)
	}
}

func TestNormalize_Nonce(t *testing.T) {
	n := Normalize(errors.New("nonce too low"))
	if n.Code != "BUNDLER_NONCE_CONFLICT" || !n.Retryable || n.Status != 409 {
		t.Errorf("got %+v", n)
	}
}

func TestNormalize_InsufficientFunds(t *testing.T) {
	n := Normalize(errors.New("insufficient funds for gas * price + value"))
	if n.Code != "INSUFFICIENT_FUNDS" || n.Retryable || n.Status != 422 {
		t.Errorf("got %+v", n)
	}
}

func TestNormalize_TimeoutAndNetwork(t *testing.T) {
	if n := Normalize(errors.New("context deadline exceeded")); n.Code != "RPC_TIMEOUT" || !n.Retryable {
		t.Errorf("timeout: %+v", n)
	}
	if n := Normalize(errors.New("dial tcp: connection refused")); n.Code != "RPC_NETWORK_ERROR" || !n.Retryable {
		t.Errorf("network: %+v", n)
	}
}

func TestNormalize_UnknownAndNil(t *testing.T) {
	if n := Normalize(errors.New("something odd")); n.Code != "BUNDLER_SEND_FAILED" || !n.Retryable || n.Status != 500 {
		t.Errorf("unknown: %+v", n)
	}
	if Normalize(nil) != nil {
		t.Error("nil → nil 기대")
	}
}
