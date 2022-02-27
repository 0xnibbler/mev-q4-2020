package fb

import (
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"io/ioutil"
	"os"
)

var passphrase = os.Getenv("PASSPHRASE")

func ReadKey() (common.Address, *ecdsa.PrivateKey, error) {
	b, err := ioutil.ReadFile("privatekey.json")
	if err != nil {
		return common.Address{}, nil, err
	}

	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return common.Address{}, nil, err
	}

	pub, ok := m["address"].(string)
	if !ok {
		return common.Address{}, nil, errors.New("no pub addr")
	}

	k, err := keystore.DecryptKey(b, passphrase)
	if err != nil {
		return common.Address{}, nil, err
	}

	return common.HexToAddress(pub), k.PrivateKey, nil
}
