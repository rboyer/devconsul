package cachestore

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/rboyer/safeio"
)

type Store struct {
	cacheDir string
}

func New(cacheDir string) (*Store, error) {
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, err
	}

	s := &Store{cacheDir: cacheDir}

	return s, nil
}

func (s *Store) LoadOrSaveValue(name string, fetchFn func() (string, error)) (string, error) {
	val, err := s.LoadValue(name)
	if err != nil {
		return "", err
	}
	if val != "" {
		return val, nil
	}

	val, err = fetchFn()
	if err != nil {
		return "", err
	}

	if err := s.SaveValue(name, val); err != nil {
		return "", err
	}

	return val, nil
}

func (s *Store) LoadValue(name string) (string, error) {
	fn := filepath.Join(s.cacheDir, name+".val")
	b, err := os.ReadFile(fn)
	if os.IsNotExist(err) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func (s *Store) SaveValue(name, value string) error {
	fn := filepath.Join(s.cacheDir, name+".val")
	_, err := safeio.WriteToFile(strings.NewReader(value), fn, 0644)
	return err
}

func (s *Store) DelValue(name string) error {
	fn := filepath.Join(s.cacheDir, name+".val")
	err := os.Remove(fn)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *Store) LoadStringFile(filename string) (string, error) {
	fn := filepath.Join(s.cacheDir, filename)
	b, err := os.ReadFile(fn)
	if os.IsNotExist(err) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func (s *Store) WriteStringFile(filename, contents string) error {
	fn := filepath.Join(s.cacheDir, filename)
	_, err := safeio.WriteToFile(strings.NewReader(contents), fn, 0644)
	return err
}
