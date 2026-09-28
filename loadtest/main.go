// loadtest 是 IM 服务的压测工具。
//
//	go run ./loadtest seed -users 1000 -group-size 20 -api http://localhost:8080
//	go run ./loadtest run -urls ws://localhost:8080/ws,ws://localhost:8081/ws -conns 1000 -rate 8000 -duration 60s -warmup 10s
//
// 指标定义见 loadtest/README.md，这里的计算必须与之保持一致。
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "seed":
		err = seedCmd(os.Args[2:])
	case "run":
		err = runCmd(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: loadtest seed|run [flags]   (run 'loadtest seed -h' for flags)")
	os.Exit(2)
}
