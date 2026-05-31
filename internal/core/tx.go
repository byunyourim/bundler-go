package core

import (
	"context"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/byunyourim/stablecoin-bundler/internal/signer"
)

const managedTxWaitTimeout = 90 * time.Second

// SendManagedTx 는 acc nonce 락 안에서 온체인 pending nonce를 읽어 컨트랙트 메서드를 호출하고
// 채굴을 기다린다. (create2 deploy, entrypoint deposit/withdraw 공용)
//
// nonce는 캐시하지 않고 매번 온체인을 읽어 멀티프로세스에서 충돌하지 않는다.
func (d *Deps) SendManagedTx(
	ctx context.Context,
	chainID int64,
	acc *signer.Account,
	to common.Address,
	contractABI abi.ABI,
	value *big.Int,
	method string,
	args ...any,
) (*types.Transaction, *types.Receipt, error) {
	client, err := d.Clients.Client(ctx, chainID)
	if err != nil {
		return nil, nil, err
	}

	var tx *types.Transaction
	var receipt *types.Receipt
	err = d.Locker.WithNonceLock(ctx, NonceLockKey(chainID, acc.Address().Hex()), func() error {
		nonce, err := client.PendingNonceAt(ctx, acc.Address())
		if err != nil {
			return err
		}
		opts, err := acc.Transactor(big.NewInt(chainID))
		if err != nil {
			return err
		}
		opts.Context = ctx
		opts.Nonce = new(big.Int).SetUint64(nonce)
		if value != nil {
			opts.Value = value
		}

		bc := bind.NewBoundContract(to, contractABI, client, client, client)
		t, err := bc.Transact(opts, method, args...)
		if err != nil {
			return err
		}
		tx = t

		wctx, cancel := context.WithTimeout(ctx, managedTxWaitTimeout)
		defer cancel()
		r, err := bind.WaitMined(wctx, client, t)
		if err != nil {
			return err
		}
		receipt = r
		return nil
	})
	return tx, receipt, err
}
