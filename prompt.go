package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

var stdin = bufio.NewReader(os.Stdin)

// promptString asks for a value, showing def as the default (returned as-is
// on an empty response).
func promptString(label, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", label, def)
	} else {
		fmt.Printf("%s: ", label)
	}
	line, _ := stdin.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

// promptRequired keeps asking until a non-empty value is given.
func promptRequired(label string) string {
	for {
		v := promptString(label, "")
		if v != "" {
			return v
		}
		fmt.Println("this field is required")
	}
}

// promptRequiredInt keeps asking until a positive integer is given.
func promptRequiredInt(label string) int {
	for {
		v := promptRequired(label)
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			return n
		}
		fmt.Println("enter a valid positive port number")
	}
}

func promptYesNo(label string, def bool) bool {
	suffix := "y/N"
	if def {
		suffix = "Y/n"
	}
	fmt.Printf("%s [%s]: ", label, suffix)
	line, _ := stdin.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return def
	}
	return line == "y" || line == "yes"
}
