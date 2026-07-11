package channels

// ChannelBasic tests basic channel send/receive
func ChannelBasic() int {
	ch := make(chan int, 1)
	ch <- 42
	return <-ch
}

// ChannelBuffered tests buffered channel
func ChannelBuffered() int {
	ch := make(chan int, 3)
	ch <- 1
	ch <- 2
	ch <- 3
	return <-ch + <-ch + <-ch
}

// ChannelUnbuffered tests unbuffered channel
func ChannelUnbuffered() int {
	ch := make(chan int)
	go func() {
		ch <- 42
	}()
	return <-ch
}

// ChannelClose tests channel close and ranging
func ChannelClose() int {
	ch := make(chan int, 5)
	ch <- 1
	ch <- 2
	ch <- 3
	close(ch)
	sum := 0
	for v := range ch {
		sum += v
	}
	return sum
}

// ChannelNil tests nil channel comparison
func ChannelNil() int {
	var ch chan int
	if ch == nil {
		return 1
	}
	return 0
}

// SelectDefault tests select with default case (no blocking)
func SelectDefault() int {
	ch := make(chan int)
	select {
	case v := <-ch:
		return v
	default:
		return -1
	}
}

// SelectSingleCase tests select with single case
func SelectSingleCase() int {
	ch := make(chan int, 1)
	ch <- 42
	select {
	case v := <-ch:
		return v
	}
}

// SelectMultiCase tests select with multiple cases
func SelectMultiCase() int {
	ch1 := make(chan int, 1)
	ch2 := make(chan int, 1)
	ch1 <- 10
	select {
	case v := <-ch1:
		return v
	case v := <-ch2:
		return v
	}
}

// SelectSendReceive tests select with both send and receive
func SelectSendReceive() int {
	ch1 := make(chan int, 1)
	ch2 := make(chan int, 1)
	ch1 <- 10
	select {
	case v := <-ch1:
		ch2 <- v * 2
		return v
	case ch1 <- 20:
		return 0
	}
}

// SelectLoop tests select in a loop
func SelectLoop() int {
	ch := make(chan int, 3)
	ch <- 1
	ch <- 2
	ch <- 3
	close(ch)
	sum := 0
	for {
		select {
		case v, ok := <-ch:
			if !ok {
				return sum
			}
			sum += v
		}
	}
}

// SelectMultipleChannels tests selecting from multiple channels
func SelectMultipleChannels() int {
	ch1 := make(chan int, 1)
	ch2 := make(chan int, 1)
	ch1 <- 10
	ch2 <- 20
	count := 0
	for i := 0; i < 2; i++ {
		select {
		case v := <-ch1:
			count += v
		case v := <-ch2:
			count += v
		}
	}
	return count
}

// ChannelDirectionSend tests send-only channel
func ChannelDirectionSend() int {
	ch := make(chan int, 1)
	var sendCh chan<- int = ch
	sendCh <- 42
	return <-ch
}

// ChannelDirectionReceive tests receive-only channel
func ChannelDirectionReceive() int {
	ch := make(chan int, 1)
	ch <- 42
	var recvCh <-chan int = ch
	return <-recvCh
}

// ChannelStruct tests channel holding struct values
func ChannelStruct() int {
	type Pair struct{ x, y int }
	ch := make(chan Pair, 1)
	ch <- Pair{10, 20}
	p := <-ch
	return p.x + p.y
}

// ChannelStructPointer tests channel holding struct pointers
func ChannelStructPointer() int {
	type Node struct{ val int }
	ch := make(chan *Node, 1)
	ch <- &Node{42}
	return (<-ch).val
}

// SliceOfChannels tests slice of channels
func SliceOfChannels() int {
	ch1 := make(chan int, 1)
	ch2 := make(chan int, 1)
	ch1 <- 10
	ch2 <- 20
	chs := []chan int{ch1, ch2}
	return <-chs[0] + <-chs[1]
}

// MapOfChannels tests map of channels
func MapOfChannels() int {
	ch1 := make(chan int, 1)
	ch2 := make(chan int, 1)
	ch1 <- 10
	ch2 <- 20
	m := map[string]chan int{"a": ch1, "b": ch2}
	return <-m["a"] + <-m["b"]
}

// ChannelDeadlock tests deadlock prevention with default
func ChannelDeadlock() int {
	ch := make(chan int)
	select {
	case <-ch:
		return 1
	default:
		return -1
	}
}

// SelectAllBlocked tests when all cases blocked but default available
func SelectAllBlocked() int {
	ch1 := make(chan int)
	ch2 := make(chan int)
	select {
	case <-ch1:
		return 1
	case <-ch2:
		return 2
	default:
		return 0
	}
}

// SelectClosedChannel tests receive from closed channel
func SelectClosedChannel() int {
	ch := make(chan int, 1)
	ch <- 42
	close(ch)
	select {
	case v, ok := <-ch:
		if ok {
			return v
		}
		return -1
	default:
		return -2
	}
}

// SelectNilChannel tests nil channel (should be skipped in select)
func SelectNilChannel() int {
	var nilCh chan int
	ch := make(chan int, 1)
	ch <- 42
	select {
	case v := <-ch:
		return v
	case <-nilCh:
		return -1
	default:
		return 0
	}
}

// ChannelPipeline tests pipeline pattern
func ChannelPipeline() int {
	ch1 := make(chan int, 2)
	ch2 := make(chan int, 2)
	ch1 <- 1
	ch1 <- 2
	close(ch1)
	go func() {
		for v := range ch1 {
			ch2 <- v * 2
		}
		close(ch2)
	}()
	sum := 0
	for v := range ch2 {
		sum += v
	}
	return sum
}

// SelectWithAssignment tests select with assignment
func SelectWithAssignment() int {
	ch := make(chan int, 1)
	ch <- 42
	v, ok := 0, false
	select {
	case v, ok = <-ch:
		if ok {
			return v
		}
	}
	return -1
}

// SelectBreak tests select with break
func SelectBreak() int {
	ch := make(chan int, 1)
	ch <- 42
	for i := 0; i < 10; i++ {
		select {
		case v := <-ch:
			return v + i
		default:
			continue
		}
	}
	return -1
}

// SelectContinue tests select with continue
func SelectContinue() int {
	ch := make(chan int, 1)
	sum := 0
	for i := 0; i < 5; i++ {
		select {
		case v := <-ch:
			sum += v
			ch <- v + 1
		default:
			if i == 0 {
				ch <- 0
			}
			continue
		}
	}
	return sum
}

// ChannelFullCap tests when channel is at capacity
func ChannelFullCap() int {
	ch := make(chan int, 2)
	ch <- 1
	ch <- 2
	full := false
	select {
	case ch <- 3:
		return 0
	default:
		full = true
	}
	if full {
		return <-ch + <-ch
	}
	return 0
}

// ChannelEmptyCap tests when channel is empty
func ChannelEmptyCap() int {
	ch := make(chan int, 2)
	empty := true
	select {
	case <-ch:
		empty = false
	default:
	}
	if empty {
		return 1
	}
	return 0
}

// SelectMutex tests channel as mutex
func SelectMutex() int {
	ch := make(chan int, 1)
	ch <- 1
	locked := true
	count := 0
	for i := 0; i < 5; i++ {
		select {
		case <-ch:
			locked = false
		default:
			if !locked {
				count++
				locked = true
				ch <- 1
			}
		}
	}
	return count
}

// ChannelTwoWay tests two-way channel communication
func ChannelTwoWay() int {
	ch := make(chan int)
	go func() {
		v := <-ch
		ch <- v * 2
	}()
	ch <- 10
	return <-ch
}

// ChannelFanIn tests fan-in pattern (multiple senders, one receiver)
func ChannelFanIn() int {
	ch := make(chan int, 3)
	go func() { ch <- 1 }()
	go func() { ch <- 2 }()
	go func() { ch <- 3 }()
	return <-ch + <-ch + <-ch
}

// ChannelBufferedsize tests different buffer sizes
func ChannelBufferedsize() int {
	sizes := []int{1, 2, 4, 8, 16}
	sum := 0
	for _, size := range sizes {
		ch := make(chan int, size)
		for i := 0; i < size; i++ {
			ch <- i
		}
		for i := 0; i < size; i++ {
			sum += <-ch
		}
	}
	return sum
}
