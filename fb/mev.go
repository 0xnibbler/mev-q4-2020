package fb

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/big"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/0xnibbler/mev-q4-2020/model"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/pkg/errors"
)

type MEV struct {
	c *rpc.Client

	nonce uint64
	id    int

	keeperAddr common.Address
	keeperKey  *ecdsa.PrivateKey

	authAddr common.Address
	authKey  *ecdsa.PrivateKey

	toAddr common.Address
}

func New(c *rpc.Client, toAddr common.Address) *MEV {
	priv, pub := genKey()

	fmt.Println("FLASHBOTS AUTH KEY\n", pub.Hex(), "\n", priv.D.String())

	keeperAddr, keeperKey, err := ReadKey()
	if err != nil {
		panic(err)
	}

	m := &MEV{
		c:          c,
		keeperAddr: keeperAddr,
		keeperKey:  keeperKey,
		authAddr:   pub,
		authKey:    priv,
		toAddr:     toAddr,
	}

	n, err := m.getNonce()
	if err != nil {
		panic(err)
	}
	m.nonce = n

	return m
}

type Exec struct {
	lock sync.Mutex
	run  bool
	a    *abi.ABI

	M *MEV
}

func (e *Exec) Running() bool {
	e.lock.Lock()
	defer e.lock.Unlock()
	return e.run
}

var contractAbi = []byte("")

func (e *Exec) Run(ctx context.Context, c *model.Cycle) (*model.RunResult, error) {
	e.lock.Lock()
	defer e.lock.Unlock()
	if e.run {
		return nil, errors.New("running")
	}

	e.run = true
	defer func() { e.run = false }()

	if e.a == nil {
		if a, err := abi.JSON(bytes.NewReader(contractAbi)); err != nil {
			return nil, err
		} else {
			e.a = &a
		}
	}

	data, err := e.a.Pack("")
	if err != nil {
		return nil, errors.Wrap(err, "ch.a.Pack")
	}

	ok, err := e.M.sendBundle(ctx, data)
	return &model.RunResult{
		Success:     ok,
		Error:       err,
	}, err
}

type bundleRequest struct {
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
	ID      int           `json:"id"`
	JsonRPC string        `json:"jsonrpc"`
}

func (m *MEV) callBundle(ctx context.Context, data []byte) error {
	start := time.Now()
	txbb, err := m.txBytes(m.toAddr, data)
	if err != nil {
		return err
	}

	targetBlockNum, err := ethclient.NewClient(m.c).BlockByNumber(ctx, nil)
	if err != nil {
		return err
	}

	br := &bundleRequest{
		Method: "eth_callBundle",
		Params: []interface{}{
			[]interface{}{hexutil.Bytes(txbb).String()},
			fmt.Sprintf("0x%x", targetBlockNum.NumberU64()+1),
			"latest"},
		ID:      m.id,
		JsonRPC: "2.0",
	}
	resp, err := m.send(br)
	if err != nil {
		return err
	}

	r := resp.(BundleCallRes)

	gp, _ := strconv.Atoi(r.BundleGasPrice)
	var gu int64
	if gp > 0 && len(r.Results) == 1 {
		gu = r.Results[0].GasUsed
	}
	fmt.Println("eth_callBundle  GP =", gp/1e+9, "gwei  GU =", gu)
	fmt.Printf("%#v  start=%v\n", r, time.Now().Sub(start))
	return nil
}

func (m *MEV) sendBundle(ctx context.Context, data []byte) (bool, error) {
	txbb, err := m.txBytes(m.toAddr, data)
	if err != nil {
		return false, err
	}

	block, err := ethclient.NewClient(m.c).BlockByNumber(ctx, nil)
	if err != nil {
		return false, err
	}

	targetBlockNum := block.NumberU64() + 1

	br := &bundleRequest{
		Method: "eth_sendBundle",
		Params: []interface{}{
			[]interface{}{hexutil.Bytes(txbb).String()},
			fmt.Sprintf("0x%x", targetBlockNum),
			0, 0},
		ID:      m.id,
		JsonRPC: "2.0",
	}

	m.id++

	if _, err := m.send(br); err != nil {
		return false, err
	}

	return m.waitForTx(targetBlockNum)
}

func (m *MEV) waitForTx(targetBlock uint64) (bool, error) {
	ch := make(chan *types.Header)
	subs, err := ethclient.NewClient(m.c).SubscribeNewHead(context.Background(), ch)
	if err != nil {
		return false, err
	}

	defer subs.Unsubscribe()
	for {
		select {
		case err := <-subs.Err():
			return false, err
		case h := <-ch:
			if h.Number.Uint64() < targetBlock {
				continue
			}

			n, err := m.getNonce()
			if err != nil {
				return false, err
			}

			if n > m.nonce {
				m.nonce = n
				return true, nil
			} else {
				return false, nil
			}
		}
	}
}

func (m *MEV) getNonce() (uint64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
	defer cancel()

	return ethclient.NewClient(m.c).NonceAt(ctx, m.keeperAddr, nil)
}

func (m *MEV) txBytes(to common.Address, data []byte) ([]byte, error) {
	rawTx := types.NewTx(&types.LegacyTx{
		Nonce:    m.nonce,
		Gas:      1500000,
		To:       &to,
		Data:     data,
		GasPrice: new(big.Int),
		Value:    new(big.Int),
	})

	signedTx, err := signTx(m.keeperKey, m.keeperAddr, rawTx)
	if err != nil {
		return nil, err
	}

	return rlp.EncodeToBytes(signedTx)
}

type BundleCallResElem struct {
	GasUsed int64       `json:"gasUsed"`
	TxHash  common.Hash `json:"txHash"`
	Value   string      `json:"value"`
}

type BundleCallRes struct {
	Id                int                 `json:"id"`
	BundleGasPrice    string              `json:"bundleGasPrice"`
	BundleHash        common.Hash         `json:"bundleHash"`
	CoinbaseDiff      string              `json:"coinbaseDiff"`
	EthSentToCoinbase string              `json:"ethSentToCoinbase"`
	GasFees           string              `json:"gasFees"`
	Results           []BundleCallResElem `json:"results"`
}

func (m *MEV) send(br *bundleRequest) (interface{}, error) {
	bb, err := json.Marshal(br)
	if err != nil {
		return nil, err
	}

	fmt.Println(string(bb))

	signature, err := ethSign(crypto.Keccak256Hash(bb).Hex(), m.authKey)
	if err != nil {
		return nil, err
	}

	fmt.Println(hexutil.Encode(signature))

	c := http.Client{Timeout: time.Second * 5}
	req, _ := http.NewRequest(http.MethodPost, "https://relay.flashbots.net", bytes.NewBuffer(bb))
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("X-Flashbots-Signature", m.authAddr.Hex()+":"+hexutil.Encode(signature))

	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		errStr := ""
		if bb, err := ioutil.ReadAll(resp.Body); err == nil {
			errStr = ": " + string(bb)
		}

		return nil, fmt.Errorf("bad status code %d%s", resp.StatusCode, errStr)
	}

	if br.Method == "eth_sendBundle" {
		return nil, nil
	}

	r := struct {
		ID      int           `json:"id"`
		Jsonrpc string        `json:"jsonrpc"`
		Result  BundleCallRes `json:"result"`
	}{}
	err = json.NewDecoder(resp.Body).Decode(&r)
	if err != nil {
		return nil, err
	}

	return r.Result, nil
}

func genKey() (*ecdsa.PrivateKey, common.Address) {
	keeperKey, err := crypto.GenerateKey()
	if err != nil {
		panic(err)
	}

	publicKey := keeperKey.Public()

	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		panic("cannot assert type: publicKey is not of type *ecdsa.PublicKey")
	}

	address := crypto.PubkeyToAddress(*publicKeyECDSA)

	return keeperKey, address
}

func signTx(key *ecdsa.PrivateKey, address common.Address, tx *types.Transaction) (*types.Transaction, error) {
	signer := types.HomesteadSigner{}

	if address != crypto.PubkeyToAddress(key.PublicKey) {
		return nil, bind.ErrNotAuthorized
	}

	signature, err := crypto.Sign(signer.Hash(tx).Bytes(), key)
	if err != nil {
		return nil, err
	}
	return tx.WithSignature(signer, signature)
}

const msgSigningPrefix = "\x19Ethereum Signed Message:\n"

func ethSign(message string, keeperKey *ecdsa.PrivateKey) ([]byte, error) {
	hash := crypto.Keccak256Hash([]byte(msgSigningPrefix + fmt.Sprintf("%d", len(message)) + message))

	signature, err := crypto.Sign(hash.Bytes(), keeperKey)
	if err != nil {
		return nil, err
	}

	return signature, nil
}
