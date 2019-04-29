package main

import (
	"encoding/json"
	"io/ioutil"

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
