// genschema writes src/contract/schema/<name>.json for every contract document (make schemas).
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/asafg8/Phosphor/src/contract"
)

func main() {
	dir := "src/contract/schema"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic(err)
	}
	for name, v := range contract.Documents {
		raw, err := contract.GenerateSchema(name, v)
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".json"), raw, 0o644); err != nil {
			panic(err)
		}
		fmt.Println(filepath.Join(dir, name+".json"))
	}
}
