package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"github.com/elwin/scionFTP/scion"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	ftp "github.com/elwin/scionFTP/client"
)

func main() {
	app := App{
		ctx: context.Background(),
	}

	app.cmd = commandMap{
		"help":    app.help,
		"connect": app.connect,
		"login":   app.login,
		"ls":      app.ls,
		"cd":      app.cd,
		"pwd":     app.pwd,
		"mode":    app.mode,
		"get":     app.retr,
		"put":     app.stor,
		"mkdir":   app.mkdir,
		"quit":    app.quit,
	}

	if err := app.run(); err != nil {
		fmt.Println(err)
	}
}

type commandMap map[string]func([]string)




var (
	local    = flag.String("local", "", "Local hostname (e.g. 1-ff00:0:110,[127.0.0.1]:4000")
	interval = time.Duration(15 * time.Second) // Interval for Keep-Alive
)

func init() {
	flag.Parse()
	if *local == "" {
		log.Fatalf("Please set the local address with -local")
	}
}

type App struct {
	conn   *ftp.ServerConn
	out    io.Writer
	cmd    commandMap
	ctx    context.Context
	cancel context.CancelFunc
}

func (app *App) print(a interface{}) {
	fmt.Fprintln(app.out, a)
}

func (app *App) run() error {
	scanner := bufio.NewReader(os.Stdin)
	app.out = os.Stdout

	for {
		fmt.Printf("> ")
		input, err := scanner.ReadString('\n')
		if err != nil {
			return err
		}

		args := strings.Split(strings.TrimSpace(input), " ")
		if f, ok := app.cmd[args[0]]; ok {
			if app.conn == nil && args[0] != "help" && args[0] != "connect" {
				app.print("Need to make a connection first using \"connect\"")
				continue
			}
			f(args[1:])
		} else {
			fmt.Printf("Command %s does not exist\n", args[0])
		}
	}
}

func (app *App) help(args []string) {
	for cmd := range app.cmd {
		app.print(cmd)
	}
}

func (app *App) connect(args []string) {
	if len(args) != 1 {
		app.print("Must supply address to connect to")
		return
	}

	conn, err := ftp.Dial(*local, args[0])
	if err != nil {
		app.print(err)
	}

	app.conn = conn

	app.conn.SetPathSelector(scion.NewRotator(10).PathSelector)

	ctx, cancel := context.WithCancel(app.ctx)
	app.cancel = cancel

	go app.keepalive(ctx, interval)
}

func (app *App) keepalive(ctx context.Context, interval time.Duration) {

	for {
		select {
		case <-time.After(interval):
			err := app.conn.NoOp()
			if err != nil {
				app.print(fmt.Sprintf("Failed to ping for keepalive: %s", err))
				return
			}
		case <-ctx.Done():
			return
		}
	}

}

func (app *App) login(args []string) {
	if len(args) != 2 {
		app.print("Must supply username and password")
		return
	}

	err := app.conn.Login(args[0], args[1])
	if err != nil {
		app.print(err)
	}
}

func (app *App) ls(args []string) {
	path := ""
	if len(args) == 1 {
		path = args[0]
	}

	entries, err := app.conn.List(path)

	if err != nil {
		app.print(err)
		return
	}

	for _, entry := range entries {
		app.print(entry.Name)
	}
}

func (app *App) cd(args []string) {
	if len(args) != 1 {
		app.print("Must supply one argument for directory change")
		return
	}

	err := app.conn.ChangeDir(args[0])
	if err != nil {
		app.print(err)
		return
	}
}

func (app *App) mkdir(args []string) {
	if len(args) != 1 {
		app.print("Must supply one argument for directory name")
		return
	}

	err := app.conn.MakeDir(args[0])
	if err != nil {
		app.print(err)
		return
	}
}

func (app *App) pwd(args []string) {
	cur, err := app.conn.CurrentDir()
	if err != nil {
		app.print(err)
	}
	app.print(cur)
}

func (app *App) mode(args []string) {
	if len(args) != 1 {
		app.print("Must supply one argument for mode, [S]tream or [E]xtended")
		return
	}

	err := app.conn.Mode([]byte(args[0])[0])
	if err != nil {
		app.print(err)
	}
}

func (app *App) retr(args []string) {
	if len(args) < 2 || len(args) > 4 {
		app.print("Must supply one argument for source and one for destination, optionally one for offset and one for length")
		return
	}

	remotePath := args[0]
	localPath := args[1]
	offset := -1
	length := -1

	var resp *ftp.Response
	var err error

	if len(args) >= 3 {
		offset, err = strconv.Atoi(args[2])
		if err != nil {
			app.print("Failed to parse offset")
			return
		}
	}

	if len(args) == 4 {
		length, err = strconv.Atoi(args[3])
		if err != nil {
			app.print("Failed to parse length")
			return
		}
	}

	fmt.Println(offset)
	fmt.Println(length)

	if offset != -1 && length != -1 {
		resp, err = app.conn.Eret(remotePath, offset, length)
	} else if offset != -1 {
		resp, err = app.conn.RetrFrom(remotePath, uint64(offset))
	} else {
		resp, err = app.conn.Retr(remotePath)
	}

	if err != nil {
		app.print(err)
		return
	}
	defer resp.Close()

	f, err := os.Create(localPath)
	if err != nil {
		app.print(err)
		return
	}
	defer f.Close()

	n, err := io.Copy(f, resp)
	if err != nil {
		app.print(err)
	} else {
		app.print(fmt.Sprintf("Received %d bytes", n))
	}
}

func (app *App) stor(args []string) {
	if len(args) != 2 {
		app.print("Must supply one argument for source and one for destination")
		return
	}

	f, err := os.Open(args[0])
	if err != nil {
		app.print(err)
		return
	}

	err = app.conn.Stor(args[1], f)
	if err != nil {
		app.print(err)
	}
}

func (app *App) quit(args []string) {
	if app.cancel != nil {
		app.cancel()
	}

	err := app.conn.Quit()
	if err != nil {
		app.print(err)
	} else {
		app.print("Goodbye")
	}

	os.Exit(0)
}