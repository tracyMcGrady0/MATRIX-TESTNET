// Copyright (c) 2018 The MATRIX Authors 
// Distributed under the MIT software license, see the accompanying
// file COPYING or http://www.opensource.org/licenses/mit-license.php
package blockgenor

import (
	"errors"
	"github.com/matrix/go-matrix/accounts"
	"github.com/matrix/go-matrix/accounts/signhelper"
	"github.com/matrix/go-matrix/common"
	"github.com/matrix/go-matrix/core"
	"github.com/matrix/go-matrix/mandb"
	"github.com/matrix/go-matrix/event"
	"github.com/matrix/go-matrix/hd"
	"github.com/matrix/go-matrix/reelection"
	"math/big"
)

var (
	TimeStampError          = errors.New("Timestamp Error")
	NodeIDError             = errors.New("Node Error")
	PosHeaderError          = errors.New("PosHeader Error")
	MinerResultError        = errors.New("MinerResult Error")
	MinerPosfail            = errors.New("MinerResult POS Fail")
	AccountError            = errors.New("Acccount Error")
	TxsError                = errors.New("txs Error")
	NoWallets               = errors.New("No Wallets ")
	NoAccount               = errors.New("No Account   ")
	ParaNull                = errors.New("Para is null  ")
	Noleader                = errors.New("not leader  ")
	SignaturesError         = errors.New("Signatures Error")
	FakeHeaderError         = errors.New("FakeHeader Error")
	VoteResultError         = errors.New("VoteResultError Error")
	HeightError             = errors.New("Height Error")
	HaveNotOKResultError    = errors.New("have no satisfy miner result")
	HaveNoGenBlockError     = errors.New("have no gen block data")
	HashNoSignNotMatchError = errors.New("hash without sign not match")
	maxUint256              = new(big.Int).Exp(big.NewInt(2), big.NewInt(256), big.NewInt(0))
)

type Backend interface {
	AccountManager() *accounts.Manager
	BlockChain() *core.BlockChain
	TxPool() *core.TxPool
	ChainDb() mandb.Database
	EventMux() *event.TypeMux
	SignHelper() *signhelper.SignHelper
	HD() *hd.HD
	ReElection() *reelection.ReElection
	FetcherNotify(hash common.Hash, number uint64)
}
