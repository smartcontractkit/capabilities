package main

import (
	"fmt"
	"os"

	"github.com/andybalholm/brotli"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: compress_wasm <wasm_file> <output_file>")
		os.Exit(1)
	}

	wasmFile := os.Args[1]
	outputFile := os.Args[2]

	wasmBytes, err := os.ReadFile(wasmFile)
	if err != nil {
		panic(err)
	}

	output, err := os.Create(outputFile)
	if err != nil {
		panic(err)
	}
	defer output.Close()

	writer := brotli.NewWriter(output)
	defer writer.Close()

	_, err = writer.Write(wasmBytes)
	if err != nil {
		panic(err)
	}

	fmt.Printf("Compressed module written to %s\n", outputFile)
}
