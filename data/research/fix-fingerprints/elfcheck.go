package main

import (
	"bytes"
	"debug/elf"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

var syms = []string{"zend_mm_safe_error_ex", "zend_new_array_singleton", "zend_set_memory_hard_limit_percent"}
var strs = []string{"Hard memory limit of"}

func main() {
	files, _ := filepath.Glob(os.Args[1] + "/*")
	sort.Strings(files)
	for _, f := range files {
		ef, err := elf.Open(f)
		if err != nil {
			continue
		}
		def := map[string]bool{}
		if ds, err := ef.DynamicSymbols(); err == nil {
			for _, s := range ds {
				if s.Section != elf.SHN_UNDEF {
					def[s.Name] = true
				}
			}
		}
		ro := []byte{}
		if s := ef.Section(".rodata"); s != nil {
			ro, _ = s.Data()
		}
		out := fmt.Sprintf("%-28s", filepath.Base(f))
		for _, n := range syms {
			out += fmt.Sprintf(" %s=%v", n[5:12], def[n])
		}
		for _, n := range strs {
			out += fmt.Sprintf(" rodata[%q]=%v", n[:4], bytes.Contains(ro, []byte(n)))
		}
		fmt.Println(out)
		ef.Close()
	}
}
