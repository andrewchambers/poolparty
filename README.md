# poolparty

An http server that creates a dynamic pool of workers and dispatches requests to them.
Currently poolparty is primarily used for janet, but this may change in the future.

# Quick example

webapp.janet
```
(import poolparty)

(defn handler [req]
  @{:status 200
    :body "ok!"})

(defn main [&]
  (poolparty/serve handler))
```

Then launch pool party from the command line:

```
$ poolparty -- janet ./example/app.janet
```
or
```
$ poolparty --max-pool-size 8 -- janet ./example/app.janet
```

To restart the worker pool:

```
$ echo -e "restart-workers\nexit" | nc -U ./poolparty.sock
```

# Building

Parts of poolparty are implemented in go, to build this you need a recent go compiler, jpm knows how to invoke go:
```
$ cd poolparty
$ jpm --verbose install
...
```

# Ctl socket

Poolparty is controlled by a simple unix socket protocol:

To connect:

```
$ nc -U poolparty.sock
```

Then try typing any of the following commands:

- restart-workers : Restart all workers with zero downtime.
- spawn-workers N : N workers.
- remove-workers N : Kill up to N workers, down to the pool minimum.
- stats : Print human readable stats.
- collectd-metrics INTERVAL : Print collectd exec format metrics forever.
- exit : Disconnect.

# Poolparty <-> Worker protocol

Poolparty communicates requests with workers one at a time, a request is first written to the worker's stdin and once that request is handled, the worker must write a response to file descriptor 3 (chosen to separate it from application logging to stderr or stdout).

Pool party workers request and response packets follow a simple length prefix format:

```
size: int32 # little endian
payload: data<size> # fixed size byte array
```

In this format the request/response payload is encoded following this [BARE](https://baremessages.org) schema:

```

type HTTPRequest {
  remote_address: string
  uri: string
  method: string
  headers: map[string]string
  body: data
}

type HealthCheckRequest {}

type Request = HTTPRequest | HealthCheckRequest | ... Reserved

type HTTPResponse {
  status: uint
  headers: map[string][]string
  body: data
}

type Response = HTTPResponse | ... Reserved

```

