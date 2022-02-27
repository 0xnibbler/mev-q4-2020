package util

import (
	"encoding/json"
	"os"
	"path"
)

const dataDir = "data"

func Save(file string, data interface{}) error {
	if _, err := os.Stat(dataDir); err == os.ErrNotExist {
		if err := os.Mkdir(dataDir, os.ModeDir); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	p := path.Join(dataDir, file+".json")

	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return err
	}

	defer f.Close()

	e := json.NewEncoder(f)
	e.SetIndent("", "\t")

	return e.Encode(data)
}

func Load(file string, data interface{}) error {
	f, err := os.Open(path.Join(dataDir, file+".json"))
	if err != nil {
		return err
	}

	defer f.Close()
	return json.NewDecoder(f).Decode(data)
}
