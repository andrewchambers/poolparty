package textctl

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/anmitsu/go-shlex"
)

type CommandHandler interface {
	Handle(command string, args []string, w io.Writer) error
}

type HandlerFunc func(string, []string, io.Writer) error

func (h HandlerFunc) Handle(command string, args []string, w io.Writer) error {
	return h(command, args, w)
}

func truncateLine(l string) string {
	eol := strings.IndexRune(l, '\n')
	if eol != -1 {
		l = l[:eol]
	}
	return l
}

func Serve(l net.Listener, handler CommandHandler) error {
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		go ServeOne(c, handler)
	}
}

func ServeOne(rwc io.ReadWriteCloser, handler CommandHandler) error {
	defer rwc.Close()
	w := io.Writer(rwc)
	s := bufio.NewScanner(rwc)
	argBuf := bytes.Buffer{}

	for s.Scan() {

		l := s.Bytes()

		if len(l) == 0 && argBuf.Len() == 0 {
			continue
		}

		if len(l) != 0 && l[len(l)-1] == '\\' {
			_, err := argBuf.Write(l[0 : len(l)-1])
			if err != nil {
				return err
			}
			err = argBuf.WriteByte('\n')
			if err != nil {
				return err
			}
			continue
		}

		_, err := argBuf.Write(l)
		if err != nil {
			return err
		}

		splitArgs, err := shlex.Split(argBuf.String(), true)

		if err == nil {
			if len(splitArgs) != 0 {
				if splitArgs[0] == "exit" {
					return nil
				}
				err = handler.Handle(splitArgs[0], splitArgs[1:], w)
			}
		}

		if err != nil {
			_, err = fmt.Fprintf(rwc, "ERROR: %s\n", truncateLine(err.Error()))
		} else {
			_, err = fmt.Fprint(rwc, "OK\n")
		}
		if err != nil {
			return err
		}

		argBuf.Reset()
	}

	return s.Err()
}
