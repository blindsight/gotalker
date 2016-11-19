package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"golang.org/x/net/websocket"
)

const (
	syserror       = "Sorry, a system error has occured"
	defaultCommand = "say"
	configFile     = "datafiles/config.json"
	comTemplates   = "comfiles"
	userDescLen    = 40
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
type config struct {
	Mainport      int  `json:"main_port"`
	Webport       int  `json:"web_port"`
	MaxUsers      int  `json:"max_users"`
	LoginIdleTime int  `json:"login_idle_time"`
	UserIdleTime  int  `json:"user_idle_time"`
	StopLogins    bool `json:"stop_logins"`
}

type system struct {
	OnlineCount int
	LoginCount  int
	sync.Mutex
}

var commandTemplates map[string]*template.Template

type User struct {
	Name        string
	Description string
	Login       uint8
	Socket      net.Conn
	WebSocket   *websocket.Conn
	LastInput   time.Time
	SocketType  uint8
	sync.Mutex
}

func (u *User) Disconnect() {
	var site string
	var name string
	var loginState uint8
	u.Lock()
	if u.SocketType == SocketTypeWebSocket {
		site = u.WebSocket.RemoteAddr().String()
	} else {
		site = u.Socket.RemoteAddr().String()
	}
	name = u.Name
	loginState = u.Login
	u.Unlock()

	if loginState == 0 {
		u.Write("\nYou are removed from this reality...\n\n")
		u.Write(fmt.Sprintf("You were logged on from site %s\n", site))
		writeWorld(userList, fmt.Sprintf("[Leaving is: %s]\n", name))
	}
	u.Close()

	talkerSystem.Lock()
	talkerSystem.OnlineCount--
	talkerSystem.Unlock()
}

func (u *User) Write(str string) {
	u.Lock()
	//more will be added to this over time
	if u.SocketType == SocketTypeWebSocket {
		websocket.Message.Send(u.WebSocket, str)
		//u.WebSocket.Write([]byte(str))
	} else {
		u.Socket.Write([]byte(str))
	}
	u.Unlock()
}

func (u *User) Close() {
	u.Lock()
	if u.SocketType == SocketTypeWebSocket {
		u.WebSocket.Close()
	} else {
		u.Socket.Close()
	}
	u.Unlock()
}

type users []*User

var userList users

var userListLock sync.Mutex

func (ulist *users) AddUser(u *User) {
	userListLock.Lock()
	*ulist = append(*ulist, u)
	userListLock.Unlock()
}

func (ulist *users) RemoveUser(u *User) {
	userListLock.Lock()
	connIndex := -1
	for i, currentConn := range *ulist {
		if currentConn == u {
			connIndex = i
		}
	}
	if connIndex > -1 {
		//TODO: how to deal with this in a safe way?
		*ulist = append((*ulist)[:connIndex], (*ulist)[connIndex+1:]...)
	}
	userListLock.Unlock()
}

func NewUser() (*User, error) {
	u := User{}
	u.Login = LoginName
	return &u, nil
}

var commands map[string]func(*User, string) bool
var talkerSystem *system
var talkerConfig *config

func main() {
	var configLocation string
	if len(os.Args) > 1 {
		configLocation = os.Args[1]
	} else {
		configLocation = configFile
	}

	readContents, err := ioutil.ReadFile(configLocation)
	publicDirectory := "public"

	fmt.Printf("Parsing config file '%s'...\n", configLocation)
	if err != nil {
		panic(fmt.Sprintf("Cannot open config file: %s", err.Error()))
	}

	err = json.Unmarshal(readContents, &talkerConfig)
	if err != nil {
		panic(fmt.Sprintf("Unable to read config file: %s", err.Error()))
	}

	ln, err := net.Listen("tcp", ":"+strconv.Itoa(talkerConfig.Mainport))

	if err != nil {
		fmt.Println("error setting up socket")
	}
	userList = users{}
	talkerSystem = &system{}
	fmt.Println("/------------------------------------------------------------\\")
	fmt.Printf(" GoTalker server booting %s\n", time.Now().Format(time.ANSIC))
	fmt.Println("|-------------------------------------------------------------|")

	fmt.Println("Parsing command structure")
	commands = map[string]func(*User, string) bool{
		"desc": func(u *User, inpstr string) bool {
			u.Lock()
			currentDescription := u.Description
			u.Unlock()
			if inpstr == "" {
				u.Write(fmt.Sprintf("Your current description is: %s\n", currentDescription))
				return false

			}
			if len(inpstr) > userDescLen {
				u.Write("Description too long.\n")
				return false
			}
			u.Lock()
			u.Description = inpstr
			u.Unlock()
			u.Write("Description set.\n")
			return false
		},
		"help": func(u *User, inpstr string) bool {
			u.Write("\n+----------------------------------------------------------------------------+\n")
			u.Write("   All commands start with a '.'                                                \n")
			u.Write("+----------------------------------------------------------------------------+\n")

			var output string
			count := 0
			for key := range commands {
				count++
				output += fmt.Sprintf("%11s", key)

				if count%5 == 0 {
					output += "\n"
				}
			}
			if count%5 != 0 {
				output += "\n"
			}
			u.Write(output)
			u.Write("+----------------------------------------------------------------------------+\n")
			u.Write(fmt.Sprintf(" There is a total of %d commands that you can use\n", count))
			u.Write("+----------------------------------------------------------------------------+\n")
			return false
		},
		"quit": func(u *User, inpstr string) bool {
			u.Disconnect()
			userList.RemoveUser(u)
			return true
		},
		"say": func(u *User, inpstr string) bool {
			if inpstr != "" {
				writeWorld(userList, u.Name+" says: "+inpstr+"\n")
			}
			return false
		},
		"who": func(u *User, inpstr string) bool {
			whoTemplate, ok := commandTemplates["who"]
			type smallUser struct {
				NameDescription string
				DiffString      string
			}

			var whoStruct = struct {
				UserTotal int
				UserList  []smallUser
			}{}

			if !ok {
				u.Write("unable to find who template for display")
			}
			userListLock.Lock()
			for _, currentUser := range userList {
				currentUser.Lock()
				timeDifference := time.Since(currentUser.LastInput)
				diffString := time.Duration((timeDifference / time.Second) * time.Second).String()
				whoStruct.UserList = append(whoStruct.UserList, smallUser{currentUser.Name + " " + currentUser.Description, diffString})
				currentUser.Unlock()
			}
			whoStruct.UserTotal = len(userList)
			userListLock.Unlock()

			var output bytes.Buffer
			err = whoTemplate.Execute(&output, whoStruct)

			if err != nil {
				u.Write(fmt.Sprintf("template error: %s", err.Error()))
			}
			u.Write(output.String())
			return false
		},
	}
	fmt.Printf("Parsing command templates\n")
	loadCommandTemplates(comTemplates)

	fmt.Println("Setting up web layer")
	http.Handle("/", http.FileServer(http.Dir(publicDirectory)))
	http.Handle("/com", websocket.Handler(acceptWebConnection))
	fmt.Printf("Initialising weblayer on: %d\n", talkerConfig.Webport)
	fmt.Printf("Initialising socket on port: %d\n", talkerConfig.Mainport)
	fmt.Println("|-------------------------------------------------------------|")
	fmt.Printf(" Booted with PID %d\n", os.Getpid())
	fmt.Println("\\-------------------------------------------------------------/")

	go http.ListenAndServe(":"+strconv.Itoa(talkerConfig.Webport), nil)
	for {
		conn, err := ln.Accept()

		if err != nil {
			fmt.Println("unable to accept socket", err)
			continue
		}

		go acceptHTTPConnection(conn)
	}
}

func acceptWebConnection(conn *websocket.Conn) {
	u, err := NewUser()
	if err != nil {
		conn.Write([]byte(fmt.Sprintf("\n\r%s: unable to create session", syserror)))
		conn.Close()
		fmt.Printf("[acceptConnection] User Creation error: %s", err.Error())
	}
	u.WebSocket = conn
	u.SocketType = SocketTypeWebSocket
	acceptConnection(u)
}

func acceptHTTPConnection(conn net.Conn) {
	u, err := NewUser()
	if err != nil {
		conn.Write([]byte(fmt.Sprintf("\n\r%s: unable to create session", syserror)))
		conn.Close()
		fmt.Printf("[acceptConnection] User Creation error: %s", err.Error())
	}
	u.Socket = conn
	u.SocketType = SocketTypeNetwork
	acceptConnection(u)
}

func acceptConnection(u *User) {
	if talkerConfig.StopLogins {
		u.Write("\n\rSorry, but no connections can be made at the moment.\n\rPlease try later\n\n\r")
		u.Disconnect()
		userList.RemoveUser(u)
		return
	}

	talkerSystem.Lock()
	OnlineUsers := talkerSystem.OnlineCount + talkerSystem.LoginCount
	talkerSystem.Unlock()

	if OnlineUsers >= talkerConfig.MaxUsers {
		u.Write("\n\rSorry, but we cannot accept any more connections at this moment.\n\rPlease try again later\n\n\r")
		u.Disconnect()
		userList.RemoveUser(u)
		return
	}

	talkerSystem.Lock()
	talkerSystem.LoginCount++
	talkerSystem.Unlock()
	handleUser(u)
}

func connectUser(u *User) {
	talkerSystem.Lock()
	talkerSystem.LoginCount--
	talkerSystem.OnlineCount++
	talkerSystem.Unlock()
}

func handleUser(u *User) {
	buffer := make([]byte, 2048)
	u.Lock()
	u.LastInput = time.Now()
	u.Unlock()
	login(u, "")

	//since this is the main loop go won't clean this up. should this be moved some where else?
	logimTimeDuration := int64(time.Minute) * int64(talkerConfig.LoginIdleTime)
	loginTimer := time.NewTimer(time.Duration(logimTimeDuration))
	go func() {
		<-loginTimer.C
		u.Lock()
		since := time.Since(u.LastInput)
		loginStage := u.Login
		u.Unlock()
		if u != nil && loginStage == LoginName && int(since.Minutes()) >= talkerConfig.LoginIdleTime {
			u.Write("\n\n*** Time out ***\n\n")
			u.Disconnect()
			userList.RemoveUser(u)
		}
	}()

	for {
		var n int
		var err error
		var text string

		if u.SocketType == SocketTypeWebSocket {
			err = websocket.Message.Receive(u.WebSocket, &text)
			text = strings.TrimSpace(text)
			n = len(text)
		} else {
			n, err = u.Socket.Read(buffer)
			text = strings.TrimSpace(string(buffer[:n]))
		}
		u.Lock()
		u.LastInput = time.Now()
		u.Unlock()

		if err != nil {
			fmt.Printf("failed to read from connection. disconnecting them. %s\n", err)
			u.Disconnect()
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
					firstWhiteSpace++
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
		u.Lock()
		u.Name = inpstr
		u.Login = LoginPasswd
		u.Unlock()

		u.Write("\nPassword:")

	case LoginPasswd:
		u.Write("\nPassword accepted:")
		u.Lock()
		u.Login = LoginLogged
		u.Unlock()
		userList.AddUser(u)
		connectUser(u)
		return
	}
}

func loadCommandTemplates(comDirectory string) {
	files, err := ioutil.ReadDir(comDirectory)
	if err != nil {
		log.Fatal(fmt.Sprintf("unable to load command templates: (%s) %s", comDirectory, err.Error()))
	}

	commandTemplates = make(map[string]*template.Template)
	for _, file := range files {
		ext := path.Ext(file.Name())
		commandName := file.Name()[:len(file.Name())-len(ext)]
		if _, ok := commands[commandName]; ok {
			commandTemplates[commandName], err = template.ParseFiles(comDirectory + "/" + file.Name())

			if err != nil {
				log.Fatal(fmt.Sprintf("unable to prase command template: %s", err))
			}
		}
	}
}
