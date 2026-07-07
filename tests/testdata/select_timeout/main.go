package select_timeout

import "time"

func SelectReceivesReadyData() int {
	data := make(chan int, 1)
	data <- 42

	select {
	case v := <-data:
		return v
	case <-time.After(time.Second):
		return -1
	}
}

func SelectTimesOutWhenNoData() int {
	data := make(chan int)

	select {
	case v := <-data:
		return v
	case <-time.After(time.Millisecond):
		return -1
	}
}
