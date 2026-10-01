package main

import (
	"flag"
	"fmt"
	"os"

	"undertow/internal/nativemodule"
)

func main() {
	dll := flag.String("dll", "", "compiled Windows x64 DLL")
	out := flag.String("out", "", "output .module path")
	name := flag.String("name", "", "module name")
	version := flag.String("version", "1.0.0", "module version")
	description := flag.String("description", "", "description")
	flag.Parse()
	if *dll == "" || *out == "" || *name == "" { fmt.Fprintln(os.Stderr, "usage: nativepack -dll FILE.dll -out FILE.module -name NAME [-version VERSION] [-description TEXT]"); os.Exit(2) }
	payload, err := os.ReadFile(*dll)
	if err == nil {
		var module []byte
		module, err = nativemodule.Pack(nativemodule.Metadata{Name: *name, Version: *version, Runtime: "native", OS: "windows", Arch: "amd64", ABI: nativemodule.ABIv1, Description: *description}, payload)
		if err == nil { err = os.WriteFile(*out, module, 0600) }
	}
	if err != nil { fmt.Fprintln(os.Stderr, "nativepack:", err); os.Exit(1) }
}
