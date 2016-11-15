package main

import (
	"fmt"
	"net"
	"strconv"
)

func main() {
	port := 2000

	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))

	if err != nil {
		fmt.Println("error setting up socket")
	}

	fmt.Println("/------------------------------------------------------------\\")
	fmt.Println(" Talker setting up on port " + strconv.Itoa(port))
	fmt.Println("\\------------------------------------------------------------/")

	for {
		conn, err := ln.Accept()

		if err != nil {
			fmt.Println("unable to accept socket", err)
			continue
		}

		go acceptConnection(conn)
	}
}

func acceptConnection(conn net.Conn) {

	message := []byte("Thank you\n")

	n, err := conn.Write(message)

	if err != nil {
		fmt.Println("unable to write message to connection ", n)
	}

	go handleInput(conn)
	//conn.Close()
}

func handleInput(conn net.Conn) {
	buffer := make([]byte, 2048)

	for {
		n, err := conn.Read(buffer)

		if err != nil {
			fmt.Println("failed to read from connection. disconnecting them.")
			conn.Close()
		}

		fmt.Println("client Input: " + string(buffer))

		for i := 0; i < n; i++ {
			//resetting input buffer
			buffer[i] = 0x00
		}
	}
}
