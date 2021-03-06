package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
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
	notloggedon    = "There is no one of that name logged on."
	defaultCommand = "say"
	configFile     = "datafiles/config.json"
	colorCodeFile  = "datafiles/colorCodes.json"
	comTemplates   = "comfiles"
	motdFiles      = "motds/"
	userFiles      = "userfiles/"
	userDescLen    = 40
	userNameMin    = 3
	userNameLenMax = 16
	recapNameMax   = userNameLenMax*4 + 3
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
	Motd1Count  int
	Motd2Count  int
	sync.Mutex
}

type colorCodes struct {
	TextCode   string `json:"textCode"`
	EscapeCode string `json:"escapeCode"`
}

type messageHistory struct {
	user    string
	event   time.Time
	message string
}

var colorCodesList []colorCodes

var commandTemplates map[string]*template.Template

type User struct {
	Name        string          `json:"name"`
	Recap       string          `json:"recap"`
	Description string          `json:"description"`
	Login       uint8           `json:"-"`
	Socket      net.Conn        `json:"-"`
	WebSocket   *websocket.Conn `json:"-"`
	LastInput   time.Time       `json:"last_input"`
	SocketType  uint8           `json:"-"`
	PastTells   []*messageHistory
	sync.Mutex  `json:"-"`
}

func NewUser() (*User, error) {
	u := User{}
	u.Login = LoginName
	return &u, nil
}

func LoadFromFile(filepath string) (*User, error) {
	u := &User{}

	data, err := ioutil.ReadFile(filepath)
	if err != nil {
		return nil, err
	}

	err = json.Unmarshal(data, u)

	if err != nil {
		return nil, err
	}

	return u, nil
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
	name = u.Recap
	loginState = u.Login
	u.Unlock()

	if loginState == 0 {
		u.Write("\nYou are removed from this reality...\n\n")
		u.Write(fmt.Sprintf("You were logged on from site %s\n", site))
		writeWorld(userList, fmt.Sprintf("[Leaving is: %s]\n", name))
	}
	u.Close()

	err := u.SaveToFile(userFiles + u.Name + ".json")
	if err != nil {
		fmt.Printf("unable to save user file for '%s': %s \n", u.Name, err.Error())
	}
	talkerSystem.Lock()
	talkerSystem.OnlineCount--
	talkerSystem.Unlock()
}

func (u *User) Write(str string) {
	var output []rune
	wait := 0

	//what's the better way to do this? too many cases.. not well thought out
	for index, char := range str {
		if wait > 0 {
			wait--
			continue
		}

		if char == '^' && len(str) < index+1 && str[index+1] == '~' {
			output = append(output, char)
			output = append(output, '~')
			wait = 1
		} else if char == '~' {
			colorFind := str[index+1 : index+3]
			foundCode := false

			for i := 0; i < len(colorCodesList); i++ {
				if colorFind == colorCodesList[i].TextCode {
					output = append(output, []rune(colorCodesList[i].EscapeCode)...)
					foundCode = true
					wait = 2
					break
				}
			}

			if foundCode == false {
				output = append(output, char)
			}
		} else {
			output = append(output, char)
		}
	}

	//0 is assumed to be the escape character
	output = append(output, []rune(colorCodesList[0].EscapeCode)...)

	u.Lock()
	//more will be added to this over time
	if u.SocketType == SocketTypeWebSocket {
		websocket.Message.Send(u.WebSocket, string(output))
		//u.WebSocket.Write([]byte(str))
	} else {
		u.Socket.Write([]byte(string(output)))
	}
	u.Unlock()
}

func (u *User) SaveToFile(savePath string) error {
	data, err := json.Marshal(u)
	if err != nil {
		return err
	}

	_, err = os.Stat(path.Dir(savePath))
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err = os.Mkdir(path.Dir(savePath), 0700); err != nil {
			return err
		}
	}

	err = ioutil.WriteFile(savePath, data, 0600)
	if err != nil {
		return err
	}

	return nil
}

func (u *User) Tell(fromUser *User, message string) {
	u.Lock()
	fromUser.Lock()
	fullMessage := fmt.Sprintf("%s tells you~RS: %s\n", u.Recap, message)
	fullFromMessage := fmt.Sprintf("you tell %s~RS: %s\n", fromUser.Recap, message)

	u.PastTells = append(u.PastTells, &messageHistory{fromUser.Name, time.Now(), fullFromMessage})
	fromUser.PastTells = append(fromUser.PastTells, &messageHistory{u.Name, time.Now(), fullMessage})
	fromUser.Unlock()
	u.Unlock()

	fromUser.Write(fullMessage)
	u.Write(fullFromMessage)
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

func (ulist *users) FindByUserName(username string) (*User, error) {
	var foundUser *User
	userListLock.Lock()
	for _, u := range *ulist {
		u.Lock()
		if u.Name == username {
			foundUser = u
		}
		u.Unlock()
	}
	userListLock.Unlock()
	if foundUser == nil {
		return nil, errors.New("unable to find user")
	}

	return foundUser, nil
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

	readContents, err = ioutil.ReadFile(colorCodeFile)
	err = json.Unmarshal(readContents, &colorCodesList)
	if err != nil {
		fmt.Println(fmt.Sprintf("unable to read color codes: %s", err.Error()))
	}

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
		"revtell": func(u *User, inpstr string) bool {
			u.Write("\n~BB~FG*** Your Tell buffer ***\n")
			if len(u.PastTells) == 0 {
				u.Write("Revtell buffer is empty.\n")
				return false
			}

			var tellHistory string
			u.Lock()
			for _, tellMessage := range u.PastTells {
				tellHistory += tellMessage.message
			}
			u.Unlock()
			u.Write(tellHistory)
			u.Write("\n~BB~FG*** End ***\n\n")
			return false
		},
		"say": func(u *User, inpstr string) bool {
			if inpstr != "" {
				writeWorld(userList, u.Recap+" says: "+inpstr+"\n")
			}
			return false
		},
		"set": func(u *User, inpstr string) bool {
			spaceIndex := strings.Index(inpstr, " ")
			if spaceIndex == -1 {
				u.Write("set recap: set recap <name as you would like it.\n")
				//show attributes
				return false
			}
			subCommand := inpstr[:spaceIndex]
			afterCommand := inpstr[spaceIndex+1:]
			switch subCommand {
			case "recap":
				if afterCommand == "" {
					u.Write("Usage: set recap <name as you would like it.\n")
					return false
				}

				if len(afterCommand) > recapNameMax-3 {
					u.Write("The recapped name length is too long - try using fewer color codes")
					return false
				}

				u.Lock()
				name := u.Name
				u.Unlock()
				recname := colorComStrip(afterCommand)

				if len(recname) > userNameLenMax || strings.ToLower(recname) != strings.ToLower(name) {
					u.Write("The recapped name still has to match your proper name.\n")
					return false
				}
				u.Lock()
				u.Recap = afterCommand + "~RS"
				u.Unlock()
				u.Write(fmt.Sprintf("Your name will now appear as '%s~RS' on the 'who', 'examine', tells, etc\n", afterCommand))
			}

			return false
		},
		"tell": func(u *User, inpstr string) bool {
			if inpstr == "" {
				//TODO: review tells
				u.Write("Usage tell <user> <text>\n")
				return false
			} // else if only user name?

			spaceIndex := strings.Index(inpstr, " ")
			if spaceIndex == -1 { //has user but nothing else
				u.Write("Usage tell <user> <text>\n")
				//show attributes
				return false
			}
			userName := inpstr[:spaceIndex]
			message := inpstr[spaceIndex+1:]

			otherUser, err := userList.FindByUserName(userName)
			if err != nil {
				u.Write(notloggedon + "\n")
			}

			if otherUser != nil {
				if otherUser == u {
					u.Write("Talking to yourself is the first sign of madness\n")
					return false
				}
				u.Tell(otherUser, message)
			}

			return false
		},
		"think": func(u *User, inpstr string) bool {
			var name string
			u.Lock()
			name = u.Recap
			u.Unlock()

			if inpstr == "" {
				writeWorld(userList, fmt.Sprintf("%s thinks nothing--now that is just typical!\n", name))
			} else {
				writeWorld(userList, fmt.Sprintf("%s thinks . o O ( %s )\n", name, inpstr))
			}
			return false
		},
		"who": func(u *User, inpstr string) bool {
			whoTemplate, ok := commandTemplates["who"]
			type smallUser struct {
				Name        string
				Recap       string
				Description string
				DiffString  string
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
				whoStruct.UserList = append(whoStruct.UserList, smallUser{currentUser.Name, currentUser.Recap, currentUser.Description, diffString})
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

	countMotds(motdFiles)
	fmt.Printf("There %d login motds and %d post-login motds\n", talkerSystem.Motd1Count, talkerSystem.Motd2Count)

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
	var motd1Count int
	talkerSystem.Lock()
	motd1Count = talkerSystem.Motd1Count
	talkerSystem.Unlock()

	if motd1Count > 0 {
		contents, err := ioutil.ReadFile(motdFiles + "/motd1/motd" + strconv.Itoa(rand.Intn(motd1Count)) + ".tmpl")
		if err != nil {
			fmt.Printf("problem with motd1: %s\n", err.Error())
		} else {
			u.Write(string(contents))
		}

	} else {
		u.Write("Welcome to here!\n\nSorry, but the login screen sppears to be missing at this time.\n\r")
	}

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
	var name string
	var desc string
	talkerSystem.Lock()
	talkerSystem.LoginCount--
	talkerSystem.OnlineCount++
	talkerSystem.Unlock()

	u.Lock()
	name = u.Recap
	desc = u.Description
	u.Unlock()

	writeWorld(userList, fmt.Sprintf("~OL[Entering is: ~RS%s~RS %s~RS~OL]\n", name, desc))
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
		if len(inpstr) < userNameMin {
			u.Write("\nName too short.\n\n")
			return
		}
		if len(inpstr) > userNameLenMax {
			u.Write("\nName too long.\n\n")
			return
		}

		//check password..
		_, err := os.Stat(userFiles + u.Name + ".json")

		if err != nil && os.IsNotExist(err) {
			u.Write("new user...\n")
			u.Lock()
			u.Name = inpstr
			u.Recap = inpstr
			u.Login = LoginConfirm
			u.Unlock()
		} else {
			u.Lock()
			u.Name = inpstr
			u.Recap = inpstr
			u.Login = LoginPasswd
			u.Unlock()
		}

		u.Write("\nPassword:")
		return
	case LoginPasswd:
		//check password
		u.Lock()
		u.Login = LoginPrompt
		u.Unlock()
		return
	case LoginConfirm:
		u.Lock()
		u.Description = "is a newbie."
		u.SaveToFile(userFiles + u.Name + ".json")
		u.Login = LoginPrompt
		u.Unlock()
		u.Write("\n\nPress return to continue: \n\n")
		return
	case LoginPrompt:
		var motd2Count int
		talkerSystem.Lock()
		motd2Count = talkerSystem.Motd2Count
		talkerSystem.Unlock()

		if motd2Count > 0 {
			contents, err := ioutil.ReadFile(motdFiles + "/motd2/motd" + strconv.Itoa(rand.Intn(motd2Count)) + ".tmpl")
			if err != nil {
				fmt.Printf("problem with motd2: %s\n", err.Error())
			} else {
				u.Write("\n" + string(contents))
			}

		} else {
			u.Write("Welcome to here!\n\nSorry, but the post login screen sppears to be missing at this time.\n\r")
		}

		u.Write("\n\nPress return to continue: \n\n")

		u.Lock()
		u.Login = LoginLogged
		u.Unlock()
		u.Write("\n\n")
		userList.AddUser(u)
		connectUser(u)
		return
	}
}

func loadCommandTemplates(comDirectory string) {
	templateFuncs := template.FuncMap{
		"colorCount": func(format string, addTo int) int {
			return countColors(format) + addTo
		},
		"join": func(joinString string, s ...string) string {
			return strings.Join(s, joinString)
		},
	}

	files, err := ioutil.ReadDir(comDirectory)
	if err != nil {
		log.Fatal(fmt.Sprintf("unable to load command templates: (%s) %s", comDirectory, err.Error()))
	}

	commandTemplates = make(map[string]*template.Template)
	for _, file := range files {
		ext := path.Ext(file.Name())
		commandName := file.Name()[:len(file.Name())-len(ext)]
		if _, ok := commands[commandName]; ok {
			commandTemplates[commandName], err = template.New(file.Name()).Funcs(templateFuncs).ParseFiles(comDirectory + "/" + file.Name())

			if err != nil {
				log.Fatal(fmt.Sprintf("unable to prase command template: %s", err))
			}
		}
	}
}

func countMotds(motdDir string) error {
	talkerSystem.Lock()
	talkerSystem.Motd1Count = 0
	talkerSystem.Motd2Count = 0
	talkerSystem.Unlock()

	files, err := ioutil.ReadDir(motdDir + "/motd1")

	if err != nil {
		return fmt.Errorf("Directory open failure in count motds: %s", err.Error())
	}

	files2, err := ioutil.ReadDir(motdDir + "/motd2")

	if err != nil {
		return fmt.Errorf("Directory open failure in count motds: %s", err.Error())
	}

	talkerSystem.Lock()
	talkerSystem.Motd1Count = len(files)
	talkerSystem.Motd2Count = len(files2)
	talkerSystem.Unlock()

	return nil
}

func countColors(colorString string) int {
	colorCount := 0
	wait := 0
	for index, char := range colorString {
		if wait > 0 {
			wait--
			continue
		}
		if char == '^' && len(colorString) < index+1 && colorString[index+1] == '~' {
			wait = 1
			continue
		} else if char == '~' {
			colorFind := colorString[index+1 : index+3]
			for i := 0; i < len(colorCodesList); i++ {
				if colorFind == colorCodesList[i].TextCode {
					colorCount += 1 + len(colorCodesList[i].TextCode)
					wait = len(colorCodesList[i].TextCode)
					break
				}
			}
		}
	}

	return colorCount
}

func colorComStrip(str string) string {
	removedColor := ""
	wait := 0
	foundColor := false
	for index, char := range str {
		if wait > 0 {
			wait--
			foundColor = false
			continue
		}
		if char == '~' && (index == 0 || index > 0 && str[index-1] != '^') {
			colorFind := str[index+1 : index+3]
			for i := 0; i < len(colorCodesList); i++ {
				if colorFind == colorCodesList[i].TextCode {
					wait = len(colorCodesList[i].TextCode)
					foundColor = true
					break
				}
			}
		}
		if foundColor == false {
			removedColor += string(char)
		}
	}
	return removedColor
}
