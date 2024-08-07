package main

import (
	"github.com/predakanga/external-dns-configmap-provider/cmd"
)

var version = "0.0.0"

func main() {
	cmd.Execute(version)
}
