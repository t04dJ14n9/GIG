package channel_pipeline

func gen(nums ...int) <-chan int {
	out := make(chan int)
	go func() {
		defer close(out)
		for _, n := range nums {
			out <- n
		}
	}()
	return out
}

func square(in <-chan int) <-chan int {
	out := make(chan int)
	go func() {
		defer close(out)
		for n := range in {
			out <- n * n
		}
	}()
	return out
}

func SumSquares() int {
	sum := 0
	for n := range square(gen(1, 2, 3)) {
		sum += n
	}
	return sum
}
