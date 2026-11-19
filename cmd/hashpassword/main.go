// Command hashpassword prints a bcrypt hash of the password given as its argument.
// It is run by hand to produce the hashes in migrations/002_seed.sql.
package main

import (
	"fmt"
	"os"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: hashpassword <password>")
		os.Exit(2)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(os.Args[1]), bcrypt.DefaultCost)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hash password:", err)
		os.Exit(1)
	}

	fmt.Println(string(hash))
}
