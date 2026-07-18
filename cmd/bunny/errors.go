package main

import "errors"

// errHandled signals that a command already reported its failure(s) to the user
// (per-package ✗ lines plus a summary count), so main should exit non-zero
// without printing anything more. Returned when a batch had failures but no
// other, unreported error.
var errHandled = errors.New("handled")
