package app

import (
	"encoding/json"
	"io"

	"github.com/rboyer/devconsul/util"
)

// Deprecated: x
func filesExist(parent string, paths ...string) (bool, error) {
	return util.FilesExist(parent, paths...)
}

// Deprecated: x
func fileExists(path string) (bool, error) {
	return util.FileExists(path)
}

// Deprecated: x
func hashFile(path string) (string, error) {
	return util.HashFile(path)
}

// Deprecated: x
func addFileToHash(path string, w io.Writer) error {
	return util.AddFileToHash(path, w)
}

func jsonPretty(val interface{}) string {
	out, err := json.MarshalIndent(val, "", "  ")
	if err != nil {
		return "<ERROR>"
	}
	return string(out)
}
