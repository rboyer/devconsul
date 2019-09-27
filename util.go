package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"

	"github.com/hashicorp/hcl"
	"github.com/hashicorp/hcl/hcl/ast"
	hclparser "github.com/hashicorp/hcl/hcl/parser"
)

func jsonPretty(val interface{}) string {
	out, err := json.MarshalIndent(val, "", "  ")
	if err != nil {
		return "<ERROR>"
	}
	return string(out)
}

func parseHCLFile(filename string) (ast.Node, error) {
	b, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	return hclparser.Parse(b)
}

func parseHCL(b []byte) (ast.Node, error) {
	return hclparser.Parse(b)
}

func serialDecodeHCL(out interface{}, configs []string) error {
	for i, config := range configs {
		n, err := hclparser.Parse([]byte(config))
		if err != nil {
			return fmt.Errorf("could not parse snippet #%d: %v", i, err)
		}
		if err := hcl.DecodeObject(out, n); err != nil {
			return fmt.Errorf("could not decode snippet #%d: %v", i, err)
		}
	}
	return nil
}
