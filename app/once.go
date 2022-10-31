package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rboyer/safeio"
)

func runOnce(name string, fn func() error) error {
	if ok, err := hasRunOnce("init"); err != nil {
		return err
	} else if ok {
		return nil
	}

	if err := fn(); err != nil {
		return err
	}

	_, err := safeio.WriteToFile(bytes.NewReader([]byte(name)), "cache/"+name+".done", 0644)
	return err
}

func hasRunOnce(name string) (bool, error) {
	b, err := os.ReadFile("cache/" + name + ".done")
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return name == string(b), nil
}

func checkHasInitRunOnce() error {
	return checkHasRunOnce("init")
}

func checkHasRunOnce(name string) error {
	ok, err := hasRunOnce(name)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	return fmt.Errorf("'%s %s' has not yet been run", ProgramName, name)
}

func ResetRunOnceMemory() error {
	files, err := filepath.Glob("cache/*.done")
	if err != nil {
		return err
	}

	for _, fn := range files {
		err := os.Remove(fn)
		if !os.IsNotExist(err) {
			return err
		}
	}

	return nil
}
