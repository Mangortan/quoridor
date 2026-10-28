package main

import "math/rand"

func randSrc(seed int64) *rand.Rand { return rand.New(rand.NewSource(seed)) }

