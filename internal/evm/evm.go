// Package evm 은 EVM 공통 유틸이다. (TS의 create2-factory 주소계산, ABI 헬퍼 대응)
// CREATE2 주소 예측, ABI 인코딩 등. go-ethereum crypto/abi 사용.
package evm

// PredictCreate2 는 CREATE2 결정론적 주소를 계산한다.
//
// TODO(골격): keccak256(0xff ++ deployer ++ salt ++ keccak256(initCode))[12:]
// (또는 Create2Factory.computeAddress 호출).
func PredictCreate2(deployer, salt, initCodeHash string) string {
	panic("not implemented")
}
