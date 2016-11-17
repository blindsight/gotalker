package main

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/websocket"
)

const (
	SYSERROR       = "Sorry, a system error has occured"
	defaultCommand = "say"
)

const (
	LoginLogged = iota
	LoginName
	LoginPasswd
	LoginConfirm
	LoginPrompt
	SocketTypeNetwork = iota
	SocketTypeWebSocket
)

//var connections []net.Conn

type User struct {
	Name        string
	Description string
	Login       uint8
	Socket      net.Conn
	WebSocket   *websocket.Conn
	LastInput   time.Time
	SocketType  uint8
}

func (u *User) Write(str string) {
	//more will be added to this over time
	if u.SocketType == SocketTypeWebSocket {
		websocket.Message.Send(u.WebSocket, str)
		//u.WebSocket.Write([]byte(str))
	} else {
		u.Socket.Write([]byte(str))
	}
}

func (u *User) Close() {
	if u.SocketType == SocketTypeWebSocket {
		u.WebSocket.Close()
	} else {
		u.Socket.Close()
	}
}

type users []*User

var userList users

func (ulist *users) AddUser(u *User) {
	*ulist = append(*ulist, u)
}

func (ulist *users) RemoveUser(u *User) {
	connIndex := -1
	for i, currentConn := range *ulist {
		if currentConn == u {
			connIndex = i
		}
	}
	if connIndex > -1 {
		*ulist = append((*ulist)[:connIndex], (*ulist)[connIndex+1:]...)
	}
}

func NewUser() (*User, error) {
	u := User{}
	u.Login = LoginName
	return &u, nil
}

var commands map[string]func(*User, string) bool

func main() {
	port := 2000
	webPort := 2010
	publicDirectory := "var/gotalker/public"

	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))

	if err != nil {
		fmt.Println("error setting up socket")
	}

	userList = users{}
	fmt.Println("/------------------------------------------------------------\\")
	fmt.Printf(" GoTalker server booting %s\n", time.Now().Format(time.ANSIC))
	fmt.Println("|-------------------------------------------------------------|")

	fmt.Println("Parsing command structure")
	commands = map[string]func(*User, string) bool{
		"who": func(u *User, inpstr string) bool {
			u.Write("\n+----------------------+-----------+\n")
			for _, currentUser := range userList {
				timeDifference := time.Since(currentUser.LastInput)
				diffString := time.Duration((timeDifference / time.Second) * time.Second).String()
				u.Write(fmt.Sprintf("| %-20s | %9s |\n", currentUser.Name, diffString))
			}
			u.Write("+----------------------+-----------+\n")
			u.Write(fmt.Sprintf("| Users Online: %-3d %-14s |\n", len(userList), " "))
			u.Write("+----------------------+-----------+\n\n")
			return false
		},
		"say": func(u *User, inpstr string) bool {
			if inpstr != "" {
				writeWorld(userList, u.Name+" says: "+inpstr+"\n")
			}
			return false
		},
		"quit": func(u *User, inpstr string) bool {
			u.Write("quitting")
			u.Close() //disconnect user?
			userList.RemoveUser(u)
			return true
		},
	}

	fmt.Println("Setting up web layer")
	http.Handle("/", http.FileServer(http.Dir(publicDirectory)))
	http.Handle("/com", websocket.Handler(acceptWebConnection))
	go http.ListenAndServe(":"+strconv.Itoa(webPort), nil)

	fmt.Printf("Initialising weblayer on: %d\n", webPort)
	fmt.Printf("Initialising socket on port: %d\n", port)
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

func acceptWebConnection(conn *websocket.Conn) {
	u, err := NewUser()
	if err != nil {
		conn.Write([]byte(fmt.Sprintf("\n\r%s: unable to create session", SYSERROR)))
		conn.Close()
		fmt.Printf("[acceptConnection] User Creation error: %s", err.Error())
	}
	u.WebSocket = conn
	u.SocketType = SocketTypeWebSocket
	handleUser(u)
}

func acceptConnection(conn net.Conn) {
	u, err := NewUser()
	if err != nil {
		conn.Write([]byte(fmt.Sprintf("\n\r%s: unable to create session", SYSERROR)))
		conn.Close()
		fmt.Printf("[acceptConnection] User Creation error: %s", err.Error())
	}
	u.Socket = conn
	u.SocketType = SocketTypeNetwork
	handleUser(u)
}

func handleUser(u *User) {
	buffer := make([]byte, 2048)
	u.LastInput = time.Now()
	login(u, "")

	for {
		var n int
		var err error
		var text string

		if u.SocketType == SocketTypeWebSocket {
			err = websocket.Message.Receive(u.WebSocket, &text)
			n = len(text)
		} else {
			n, err = u.Socket.Read(buffer)
			text = strings.TrimSpace(string(buffer[:n]))
		}
		u.LastInput = time.Now()

		if err != nil {
			fmt.Printf("failed to read from connection. disconnecting them. %s\n", err)
			u.Close()
			userList.RemoveUser(u)
			break
		}

		fmt.Printf("client Input: '%s'\n", text)
		if u.Login > 0 {
			login(u, text)
		} else {
			var possibleCommand string

			if len(text) > 0 && text[0] == '.' {
				firstWhiteSpace := strings.Index(text, " ")

				if firstWhiteSpace != -1 {
					possibleCommand = text[1:firstWhiteSpace]
				} else {
					possibleCommand = text[1:]
					firstWhiteSpace = len(text)
				}
				text = text[firstWhiteSpace:]
			} else {
				possibleCommand = defaultCommand
			}

			if val, ok := commands[possibleCommand]; ok {
				exitLoop := val(u, text)
				if exitLoop == true {
					break
				}
			} else {
				u.Write("unknown command\n")
			}
		}

		for i := 0; i < n; i++ {
			//resetting input buffer
			buffer[i] = 0x00
		}
	}
}

func writeWorld(ulist []*User, buffer string) {
	for _, u := range ulist {
		u.Write(buffer)
	}
}

func login(u *User, inpstr string) {
	switch u.Login {
	case LoginName:
		if inpstr == "" {
			u.Write("\nGive me a name:")
			return
		}
		//TODO: run some checks on the user name
		u.Name = inpstr
		u.Write("\nPassword:")
		u.Login = LoginPasswd

	case LoginPasswd:
		u.Write("\nPassword accepted:")
		u.Login = LoginLogged
		userList.AddUser(u)
		return
	}
}
