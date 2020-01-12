package main

import (
	"fmt"
	flag "github.com/spf13/pflag"
	"net"
	"os"

	"github.com/andrewchambers/poolparty"
	"github.com/coolmsg/go-coolmsg"
)

func mustConnect(ctlSocket string) *coolmsg.Client {
	c, err := net.Dial("unix", ctlSocket)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to establish connection to the control socket: %s", err.Error())
		os.Exit(1)
	}
	client := coolmsg.NewClient(c, coolmsg.ClientOptions{})
	return client
}

func main() {

	if len(os.Args) == 1 {
		fmt.Fprintf(os.Stderr, "Expected a subcommand.")
		os.Exit(1)
	}

	copy(os.Args, os.Args[1:])
	os.Args = os.Args[:len(os.Args)-1]

	ctlSocket := flag.String("ctl-socket", "./poolparty.sock", "control socket to connect to.")

	switch os.Args[0] {
	case "restart-workers":
		flag.Parse()
		client := mustConnect(*ctlSocket)
		defer client.Close()
		_, err := client.Send(coolmsg.BOOTSTRAP_OBJECT_ID, &poolparty.RestartJanetWorkersMsg{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error sending request: %s", err.Error())
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "Expected a command, one of [restart-workers]")
		os.Exit(1)
	}
}
