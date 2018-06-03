package hello // import "example.com/hello"

import (
	"fmt"
	"rsc.io/quote"
)

// this file does not type-check

type Blah = Test

const Name = "test"

func DoIt() {
	fmt.Println("Hello, world!", quote.Hello())
}

func AFunc() string {
	return fmt.Sprintf("%v", asdf)
}
