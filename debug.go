package main

import (
	"fmt"
	"os"
)

var debugFile *os.File

func initDebug() {
	f, err := os.OpenFile("debug.log", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err == nil {
		debugFile = f
	}
}

func logDebug(format string, a ...interface{}) {
	if debugFile != nil {
		fmt.Fprintf(debugFile, format+"\n", a...)
	}
}
