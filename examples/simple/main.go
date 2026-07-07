// Package main demonstrates using gig with built-in standard library.
// No custom dependency generation is needed - just import gig/stdlib.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/t04dJ14n9/gig"
	_ "github.com/t04dJ14n9/gig/stdlib/packages" // Import gig's built-in stdlib (40+ packages)
)

func main() {
	// Example 1: Simple computation
	simpleExample()

	// Example 2: Using stdlib packages (fmt, strings, etc.)
	stdlibExample()

	// Example 3: With context timeout
	contextExample()

	// Example 4: Multi-function program
	multiFunctionExample()

	extendedExample()
}

func simpleExample() {
	fmt.Println("=== Simple Computation ===")

	source := `
package main

func Compute() int {
	sum := 0
	for i := 1; i <= 10; i++ {
		sum = sum + i
	}
	return sum
}
`

	prog, err := gig.Build(source)
	if err != nil {
		panic(err)
	}

	result, err := prog.Run("Compute")
	if err != nil {
		panic(err)
	}

	fmt.Printf("Sum of 1..10 = %v\n\n", result)
}

func stdlibExample() {
	fmt.Println("=== Using Standard Library ===")

	// This example uses fmt.Sprintf and strings.ToUpper
	source := `
package main

import "fmt"
import "strings"
import "math"

func FormatGreeting(name string) string {
	upper := strings.ToUpper(name)
	return fmt.Sprintf("Hello, %s! Pi is approximately %.2f", upper, math.Pi)
}
`

	prog, err := gig.Build(source)
	if err != nil {
		panic(err)
	}

	result, err := prog.Run("FormatGreeting", "world")
	if err != nil {
		panic(err)
	}

	fmt.Printf("Result: %v\n\n", result)
}

func contextExample() {
	fmt.Println("=== With Context Timeout ===")

	source := `
package main

import "time"

func SlowOperation() string {
	time.Sleep(100 * time.Millisecond)
	return "completed"
}
`

	prog, err := gig.Build(source)
	if err != nil {
		panic(err)
	}

	// Run with a 50ms timeout - should timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = prog.RunWithContext(ctx, "SlowOperation")
	if err != nil {
		fmt.Printf("Expected timeout: %v\n", err)
	}

	// Run with enough time - should succeed
	ctx2, cancel2 := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel2()

	result, err := prog.RunWithContext(ctx2, "SlowOperation")
	if err != nil {
		panic(err)
	}
	fmt.Printf("Result: %v\n\n", result)
}

func multiFunctionExample() {
	fmt.Println("=== Multi-Function Program ===")

	// Demonstrate multiple functions without global variables
	// (global variable assignment has a known bug)
	source := `
package main

func add(a, b int) int {
	return a + b
}

func multiply(a, b int) int {
	return a * b
}

func Compute() int {
	sum := add(10, 20)
	product := multiply(3, 4)
	return sum + product
}
`

	prog, err := gig.Build(source)
	if err != nil {
		panic(err)
	}

	result, err := prog.Run("Compute")
	if err != nil {
		panic(err)
	}
	fmt.Printf("add(10, 20) + multiply(3, 4) = %v\n\n", result)
}

func extendedExample() {
	source := `
package main

type Point struct{
	x, y int
}
func Compute() int {
	p := Point{
		x: 1,
		y: 2,
	}
	
	return p.x * p.y
}
`
	prog, err := gig.Build(source)
	if err != nil {
		panic(err)
	}

	result, err := prog.Run("Compute")
	if err != nil {
		panic(err)
	}
	fmt.Printf("Sum of point is %d", result)
}
