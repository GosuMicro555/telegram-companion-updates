package main

import (
	"fmt"

	"telegram-companion/internal/bootstrapstate"
)

func main() {
	for _, name := range bootstrapstate.SecretNames() {
		fmt.Println(name)
	}
}
