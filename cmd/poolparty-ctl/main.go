package main

import (
	"fmt"
	flag "github.com/spf13/pflag"
	"net"
	"os"

	"github.com/andrewchambers/poolparty"
	"github.com/andrewchambers/srop"
)

func mustConnect(ctlSocket string) *srop.Client {
	c, err := net.Dial("unix", ctlSocket)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to establish connection to the control socket: %s", err.Error())
		os.Exit(1)
	}
	client := srop.NewClient(c, srop.ClientOptions{})
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
		m, err := client.Send(srop.BOOTSTRAP_OBJECT_ID, &poolparty.RestartWorkersMsg{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error sending request: %s", err.Error())
			os.Exit(1)
		}
		switch m := m.(type) {
		case *poolparty.CtlError:
			fmt.Fprintf(os.Stderr, "Error processing request: %s", m.Msg)
			os.Exit(1)		
		case *srop.Ok:
		}
	default:
		fmt.Fprintf(os.Stderr, "Expected a command, one of [restart-workers]")
		os.Exit(1)
	}
}
