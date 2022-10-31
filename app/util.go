package app

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

func filesExist(parent string, paths ...string) (bool, error) {
	for _, p := range paths {
		ok, err := fileExists(filepath.Join(parent, p))
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	} else {
		return true, nil
	}
}

func addFileToHash(path string, w io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

func jsonPretty(val interface{}) string {
	out, err := json.MarshalIndent(val, "", "  ")
	if err != nil {
		return "<ERROR>"
	}
	return string(out)
}
