package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
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
)

//var connections []net.Conn

type User struct {
	Name        string
	Description string
	Login       uint8
	Socket      net.Conn
	LastInput   time.Time
}

func (u *User) Write(str string) {
	//more will be added to this over time
	u.Socket.Write([]byte(str))
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

	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))

	if err != nil {
		fmt.Println("error setting up socket")
	}

	userList = users{}
	fmt.Println("/------------------------------------------------------------\\")
	fmt.Println(" Talker setting up on port " + strconv.Itoa(port))
	fmt.Println("\\------------------------------------------------------------/")

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
			u.Socket.Close()
			userList.RemoveUser(u)
			return true
		},
	}

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

	u, err := NewUser()
	if err != nil {
		conn.Write([]byte(fmt.Sprintf("\n\r%s: unable to create session", SYSERROR)))
		conn.Close()
		fmt.Printf("[acceptConnection] User Creation error: %s", err.Error())
	}
	u.Socket = conn
	u.LastInput = time.Now()
	u.Write("Give me a name:")
	//logged in
	go handleInput(u)
}

func handleInput(u *User) {
	buffer := make([]byte, 2048)

	for {
		n, err := u.Socket.Read(buffer)
		u.LastInput = time.Now()

		if err != nil {
			fmt.Println("failed to read from connection. disconnecting them.")
			u.Socket.Close()
			break
		}
		text := strings.TrimSpace(string(buffer[:n]))
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
