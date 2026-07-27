package panic_recover

// ============================================================================
// Panic/Recover/Defer Comprehensive Tests
// ============================================================================

// ============================================================================
// Basic Panic/Recover Tests
// ============================================================================

// PanicRecoverBasic tests basic panic and recover
func PanicRecoverBasic() int {
	defer func() {
		recover()
	}()
	panic("test panic")
	return 0 // never reached
}

// PanicRecoverWithValue tests recovering panic value
func PanicRecoverWithValue() int {
	var result int
	defer func() {
		if r := recover(); r != nil {
			if s, ok := r.(string); ok && s == "expected" {
				result = 42
			}
		}
	}()
	panic("expected")
	return result
}

// PanicRecoverInt tests panic with int value
func PanicRecoverInt() int {
	var result int
	defer func() {
		if r := recover(); r != nil {
			if i, ok := r.(int); ok {
				result = i * 2
			}
		}
	}()
	panic(21)
	return result
}

// NoPanicNoRecover tests that recover returns nil when not panicking
func NoPanicNoRecover() int {
	var result int
	defer func() {
		if r := recover(); r == nil {
			result = 1
		}
	}()
	return result
}

// ============================================================================
// Defer with Panic Tests
// ============================================================================

// DeferRunsOnPanic tests that deferred functions run during panic
func DeferRunsOnPanic() int {
	result := 0
	defer func() {
		result += 10
		recover()
	}()
	result += 1
	panic("test")
	return result // never reached
}

// MultipleDefersOnPanic tests LIFO order of deferred functions during panic
func MultipleDefersOnPanic() int {
	order := 0
	result := 0
	defer func() {
		order++
		result = result*10 + order // runs third: order=3, result = 12*10+3 = 123
		recover()
	}()
	defer func() {
		order++
		result = result*10 + order // runs second: order=2, result = 1*10+2 = 12
	}()
	defer func() {
		order++
		result = result*10 + order // runs first: order=1, result = 1
	}()
	panic("test")
	return result
}

// DeferModifyBeforePanic tests deferred function sees modifications before panic
func DeferModifyBeforePanic() int {
	x := 1
	defer func() {
		x = 100
		recover()
	}()
	x = 2
	panic("test")
	return x // never reached
}

// ============================================================================
// Nested Panic/Recover Tests
// ============================================================================

// NestedPanicRecover tests panic in nested function
func NestedPanicRecover() int {
	inner := func() {
		panic("inner panic")
	}
	defer func() {
		recover()
	}()
	inner()
	return 0
}

// NestedRecover tests recover in nested defer
func NestedRecover() int {
	result := 0
	defer func() {
		defer func() {
			if r := recover(); r != nil {
				result = 100
			}
		}()
		panic("second panic")
	}()
	defer func() {
		recover()
	}()
	panic("first panic")
	return result // never reached
}

// PanicInDefer tests panic in deferred function
func PanicInDefer() int {
	result := 0
	defer func() {
		if r := recover(); r != nil {
			result = 50
		}
	}()
	defer func() {
		panic("panic in defer")
	}()
	result = 1
	return result
}

// PanicChain tests chain of panics and recovers
func PanicChain() int {
	result := 0
	defer func() {
		if r := recover(); r != nil {
			result += 100
		}
	}()
	defer func() {
		panic("second")
	}()
	defer func() {
		recover()
	}()
	panic("first")
	return result
}

// ============================================================================
// Recover Return Value Tests
// ============================================================================

// RecoverReturnsNilWhenNotPanicking tests recover returns nil outside panic
func RecoverReturnsNilWhenNotPanicking() int {
	r := recover()
	if r == nil {
		return 1
	}
	return 0
}

// RecoverReturnsPanicValue tests recover returns the panic value
func RecoverReturnsPanicValue() any {
	defer func() {
		// don't recover, let it propagate
	}()
	panic("test value")
	return nil // never reached
}

// RecoverReturnsPanicValueCheck tests recover returns the panic value
func RecoverReturnsPanicValueCheck() string {
	var result string
	defer func() {
		if r := recover(); r != nil {
			result = r.(string)
		}
	}()
	panic("hello")
	return result
}

// ============================================================================
// Named Return with Panic/Recover Tests
// ============================================================================

// NamedReturnPanicRecover tests named return value with panic/recover
func NamedReturnPanicRecover() (result int) {
	defer func() {
		if recover() != nil {
			result = 42
		}
	}()
	panic("test")
	return // never reached, but defer modifies result
}

// NamedReturnDeferModify tests defer modifying named return after panic
func NamedReturnDeferModify() (result int) {
	defer func() {
		result = 100
		recover()
	}()
	result = 1
	panic("test")
	return result
}

// ============================================================================
// Complex Panic/Recover Scenarios
// ============================================================================

// PanicInLoop tests panic inside loop with recover
func PanicInLoop() int {
	sum := 0
	for i := 0; i < 5; i++ {
		func() {
			defer func() {
				recover()
			}()
			if i == 3 {
				panic("stop at 3")
			}
			sum += i
		}()
	}
	return sum
}

// PanicInClosure tests panic inside closure
func PanicInClosure() int {
	fn := func() {
		panic("closure panic")
	}
	defer func() {
		recover()
	}()
	fn()
	return 0
}

// PanicWithStructValue tests panic with struct value
func PanicWithStructValue() int {
	type Data struct {
		Value int
		Name  string
	}
	var result int
	defer func() {
		if r := recover(); r != nil {
			if d, ok := r.(Data); ok {
				result = d.Value
			}
		}
	}()
	panic(Data{Value: 99, Name: "test"})
	return result
}

// MultiplePanicSameDefer tests multiple panics caught by same defer
func MultiplePanicSameDefer() int {
	count := 0
	for i := 0; i < 3; i++ {
		func() {
			defer func() {
				if recover() != nil {
					count++
				}
			}()
			panic("test")
		}()
	}
	return count
}

// PanicInRecursiveFunction tests panic in recursive function
func PanicInRecursiveFunction() int {
	var recursive func(int)
	var result int
	recursive = func(n int) {
		defer func() {
			if recover() != nil {
				result = n
			}
		}()
		if n <= 0 {
			panic("base")
		}
		recursive(n - 1)
	}
	recursive(5)
	return result
}

// DeferClosureCapturePanic tests closure capture with panic
func DeferClosureCapturePanic() int {
	x := 1
	defer func() {
		x = 100
		recover()
	}()
	x = 2
	panic("test")
	return x // never reached
}

// PanicInDeferWithRecoverInDefer tests panic in defer recovered by another defer
func PanicInDeferWithRecoverInDefer() int {
	result := 0
	defer func() {
		if recover() != nil {
			result = 200
		}
	}()
	defer func() {
		defer func() {
			recover()
		}()
		panic("nested")
	}()
	result = 1
	return result
}

// ============================================================================
// Recover Through Deferred Method Dispatch
// ============================================================================

type methodRecoverer struct {
	result *int
}

func (r methodRecoverer) Recover() {
	if recover() != nil {
		*r.result = 1
	}
}

type recoverMethod interface {
	Recover()
}

// RecoverWithDirectInterfaceMethod tests recover in a directly deferred
// interface method call.
func RecoverWithDirectInterfaceMethod() (result int) {
	var r recoverMethod = methodRecoverer{result: &result}
	defer r.Recover()
	panic("boom")
}

// RecoverWithConcreteMethodValue tests recover through the synthetic bound
// wrapper for a concrete method value.
func RecoverWithConcreteMethodValue() (result int) {
	r := methodRecoverer{result: &result}
	recoverMethod := r.Recover
	defer recoverMethod()
	panic("boom")
}

// RecoverWithInterfaceMethodValue tests recover through the synthetic bound
// wrapper for an interface method value.
func RecoverWithInterfaceMethodValue() (result int) {
	var r recoverMethod = methodRecoverer{result: &result}
	recoverMethod := r.Recover
	defer recoverMethod()
	panic("boom")
}

// RecoverWithMethodExpression tests recover through the synthetic thunk for
// an interface method expression.
func RecoverWithMethodExpression() (result int) {
	var r recoverMethod = methodRecoverer{result: &result}
	defer recoverMethod.Recover(r)
	panic("boom")
}

type promotedMethodRecoverer struct {
	methodRecoverer
}

// RecoverWithPromotedMethod tests recover in a directly deferred promoted
// method.
func RecoverWithPromotedMethod() (result int) {
	r := promotedMethodRecoverer{
		methodRecoverer: methodRecoverer{result: &result},
	}
	defer r.Recover()
	panic("boom")
}

func recoverThroughHelper() bool { return recover() != nil }

// RecoverHelperCannotRecover verifies that an ordinary helper call is not a
// direct recover call, even when the helper is called by a deferred closure.
func RecoverHelperCannotRecover() (result int) {
	defer func() {
		if recover() != nil {
			result = 2
		}
	}()
	defer func() {
		if recoverThroughHelper() {
			result = 1
		}
	}()
	panic("boom")
}

// RecoverOnlyInDefer tests that recover only works in defer
func RecoverOnlyInDefer() int {
	// recover() called outside defer returns nil
	r := recover()
	if r == nil {
		return 1
	}
	return 0
}

// PanicNil tests panic(nil) behavior
func PanicNil() int {
	var result int
	defer func() {
		if r := recover(); r != nil {
			result = 1
		} else {
			result = 2 // recover returns nil when panic(nil)
		}
	}()
	panic(nil)
	return result
}

// PanicWithSlice tests panic with slice value
func PanicWithSlice() int {
	var result int
	defer func() {
		if r := recover(); r != nil {
			if s, ok := r.([]int); ok && len(s) == 3 {
				result = s[0] + s[1] + s[2]
			}
		}
	}()
	panic([]int{10, 20, 30})
	return result
}

// PanicWithMap tests panic with map value
func PanicWithMap() int {
	var result int
	defer func() {
		if r := recover(); r != nil {
			if m, ok := r.(map[string]int); ok {
				result = m["key"]
			}
		}
	}()
	panic(map[string]int{"key": 42})
	return result
}

// ============================================================================
// Edge Cases
// ============================================================================

// EmptyDeferPanic tests empty defer with panic
func EmptyDeferPanic() int {
	defer func() {}()
	defer func() { recover() }()
	panic("test")
	return 0
}

// DeferOrderWithMultiplePanics tests defer order with multiple panics
func DeferOrderWithMultiplePanics() int {
	result := ""
	defer func() {
		result += "a"
		recover()
	}()
	defer func() {
		result += "b"
		defer func() {
			result += "c"
			recover()
		}()
		panic("second")
	}()
	panic("first")
	return len(result) // never reached
}

// RecoverInGoroutine tests that recover works in goroutine context
// Note: This test runs synchronously due to interpreter limitations
func RecoverInGoroutine() int {
	result := 0
	func() {
		defer func() {
			if recover() != nil {
				result = 42
			}
		}()
		panic("goroutine panic")
	}()
	return result
}

// PanicTypeAssertion tests panic from type assertion
func PanicTypeAssertion() int {
	var result int
	defer func() {
		if recover() != nil {
			result = 1
		}
	}()
	var i interface{} = "hello"
	_ = i.(int) // will panic
	return result
}

// PanicMapAccess tests panic from nil map access
func PanicMapAccess() int {
	var result int
	defer func() {
		if recover() != nil {
			result = 1
		}
	}()
	var m map[string]int
	m["key"] = 1 // will panic
	return result
}

// PanicSliceIndex tests panic from slice index out of bounds
func PanicSliceIndex() int {
	var result int
	defer func() {
		if recover() != nil {
			result = 1
		}
	}()
	s := []int{1, 2, 3}
	_ = s[10] // will panic
	return result
}

// PanicNilPointer tests panic from nil pointer dereference
func PanicNilPointer() int {
	var result int
	defer func() {
		if recover() != nil {
			result = 1
		}
	}()
	var p *int
	_ = *p // will panic
	return result
}

// DeferPanicRecoverChain tests a chain of defer/panic/recover
func DeferPanicRecoverChain() int {
	result := 0
	defer func() {
		result += 1000
		recover()
	}()
	defer func() {
		result += 100
		defer func() {
			result += 10
			recover()
		}()
		panic("inner")
	}()
	panic("outer")
}
