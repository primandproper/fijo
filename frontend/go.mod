// This file exists so the Go toolchain treats frontend/ as a module boundary
// and `./...` from the repository root never descends into node_modules, where
// npm packages occasionally ship Go files. Nothing is built from it.
module github.com/primandproper/fijo/frontend

go 1.27
