package poolparty

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"time"
)

type CtlHandler struct {
	Pool *WorkerPool
}

func (h *CtlHandler) Handle(cmd string, args []string, w io.Writer) error {

	switch cmd {
	case "restart-workers":
		if len(args) != 0 {
			return errors.New("unexpected arguments")
		}
		return h.Pool.RestartWorkers(context.Background())
	case "spawn-workers", "remove-workers":
		if len(args) != 1 {
			return errors.New("expected a single argument")
		}
		n, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return err
		}
		for i := int64(0); i < n; i++ {
			switch cmd[0] {
			case 's':
				h.Pool.SpawnWorker()
			case 'r':
				h.Pool.RemoveWorker()
			}
		}
		return nil
	case "stats":
		if len(args) != 0 {
			return errors.New("unexpected arguments")
		}
		buf := bytes.Buffer{}
		stats := h.Pool.Stats()
		_, _ = fmt.Fprintf(&buf, "goroutines=%d\n", runtime.NumGoroutine())
		_, _ = fmt.Fprintf(&buf, "workers=%d\n", stats.Workers)
		_, _ = fmt.Fprintf(&buf, "worker-restarts=%d\n", stats.WorkerRestarts)
		_, err := w.Write(buf.Bytes())
		return err
	case "collectd-metrics":
		host, _ := os.Hostname()
		if host == "" {
			host = "_unknown_"
		}
		metricsIntervalStr := "10"
		metricsLabelSuffix := ""
		switch len(args) {
		case 2:
			metricsLabelSuffix = "-" + args[1]
			fallthrough
		case 1:
			metricsIntervalStr = args[0]
			fallthrough
		case 0:
			/* nothing */
		default:
			fmt.Fprintf(w, "usage: collectd-metrics [interval] [label-suffix]")
			return errors.New("unexpected arguments")
		}
		metricsInterval, _ := strconv.Atoi(metricsIntervalStr)
		if metricsInterval <= 0 {
			metricsInterval = 10
		}
		buf := bytes.Buffer{}
		bufw := io.Writer(&buf)
		for {
			now := time.Now().Unix()
			stats := h.Pool.Stats()
			buf.Reset()
			fmt.Fprintf(bufw, "putval %s/poolparty%s/gauge-goroutines interval=%d %d:%d\n", host, metricsLabelSuffix, metricsInterval, now, runtime.NumGoroutine())
			fmt.Fprintf(bufw, "putval %s/poolparty%s/gauge-workers interval=%d %d:%d\n", host, metricsLabelSuffix, metricsInterval, now, stats.Workers)
			fmt.Fprintf(bufw, "putval %s/poolparty%s/derive-worker-restarts interval=%d %d:%d\n", host, metricsLabelSuffix, metricsInterval, now, stats.WorkerRestarts)
			_, err := w.Write(buf.Bytes())
			if err != nil {
				return err
			}
			time.Sleep(time.Duration(metricsInterval) * time.Second)
		}
	}
	return errors.New("unknown command, want restart-workers|spawn-workers|remove-workers|stats|collectd-metrics")
}
